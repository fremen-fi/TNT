package audio

import (
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"
)

// This file holds three purely time-domain dynamics processors:
//
//   1. LookaheadLimiter  — anticipatory brick-wall (conformity) limiter.
//   2. CharacterLimiter  — soft-knee limiter with caller-supplied time factors.
//   3. Compress          — full-range compressor with caller-supplied time factors.
//
// Each comes in two flavours: a path wrapper that reads a WAV, processes it and
// writes it back as 32-bit float (matching Gain in amplification.go), and a
// *Samples core that mutates stereo []float64 buffers in place so the algorithm
// is unit-testable without touching disk. Detection is stereo-linked throughout
// (a single gain is derived from max(|L|,|R|) and applied to both channels) so
// the stereo image is never pulled off-centre by the gain riding.

// onePoleCoef returns the one-pole smoothing coefficient for a time constant of
// timeMs at sampleRate. A state value multiplied by this coefficient each sample
// decays to 1/e of its distance from the target after timeMs. A non-positive
// timeMs yields 0, i.e. an instantaneous (single-sample) move.
func onePoleCoef(timeMs, sampleRate float64) float64 {
	if timeMs <= 0 {
		return 0
	}
	return math.Exp(-1.0 / (timeMs / 1000.0 * sampleRate))
}

// clampMag hard-limits x to the symmetric range [-m, m].
func clampMag(x, m float64) float64 {
	if x > m {
		return m
	}
	if x < -m {
		return -m
	}
	return x
}

// staticGainDb returns the gain (in dB, always <= 0) that the static transfer
// curve applies to an input whose level is levelDb, given a threshold, a
// compression ratio and a soft knee kneeDb wide centred on the threshold. This
// is the standard soft-knee characteristic from Reiss & McPherson, "Audio
// Effects: Theory, Implementation and Application".
//
// ratio == 1 is a no-op (slope 0); ratio == math.Inf(1) makes it a hard limiter,
// pinning everything above the threshold to the threshold. A kneeDb <= 0 gives a
// hard corner at the threshold.
func staticGainDb(levelDb, thresholdDb, ratio, kneeDb float64) float64 {
	slope := 1.0/ratio - 1.0 // 0 for ratio=1, -1 for ratio=+Inf
	over := levelDb - thresholdDb

	switch {
	case kneeDb > 0 && 2*over < -kneeDb:
		return 0
	case kneeDb > 0 && 2*math.Abs(over) <= kneeDb:
		// Quadratic blend across the knee so the curve and its first
		// derivative are continuous at both knee edges.
		x := over + kneeDb/2
		return slope * x * x / (2 * kneeDb)
	default:
		if over <= 0 {
			return 0
		}
		return slope * over
	}
}

// LookaheadLimiter applies the anticipatory brick-wall limiter at path in place,
// rewriting it as a 32-bit float WAV. thresholdDb is the ceiling (e.g. -1.0),
// lookaheadMs is how far ahead the gain reduction is allowed to start ramping in
// (also doubles as the attack time), and releaseMs governs the recovery.
func LookaheadLimiter(path string, thresholdDb, lookaheadMs, releaseMs float64) error {
	left, right, sampleRate, err := readStereo(path)
	if err != nil {
		return fmt.Errorf("lookahead limiter: %w", err)
	}
	LookaheadLimitSamples(left, right, float64(sampleRate), thresholdDb, lookaheadMs, releaseMs)
	return WriteFloat32WAV(path, sampleRate, left, right)
}

// LookaheadLimitSamples is the in-place core of LookaheadLimiter.
//
// It is a conformity limiter: the output is mathematically guaranteed never to
// exceed the threshold. The reduction needed at every sample is computed first,
// then a sliding-window minimum over the lookahead window pulls that reduction
// forward in time, so by the moment a peak is emitted the gain is already down —
// no peak slips through while the envelope is still ramping. The smoothed
// envelope is what makes that ramp gradual rather than a click-inducing step; a
// final per-sample clamp at the threshold is the brick-wall backstop that mops
// up the sub-dB overshoot smoothing can leave on the very first sample of a fast
// transient. On well-tuned lookahead the clamp engages on almost nothing, so the
// result stays transparent.
func LookaheadLimitSamples(left, right []float64, sampleRate, thresholdDb, lookaheadMs, releaseMs float64) {
	n := min(len(left), len(right))
	if n == 0 {
		return
	}
	thresh := DbToLinear(thresholdDb)

	look := int(math.Round(lookaheadMs / 1000.0 * sampleRate))
	look = max(1, look)
	look = min(n-1, look)

	// required[i] is the gain that tames the inter-sample (TRUE) peak at sample i to
	// the threshold. Detection runs through the BS.1770 4× polyphase FIR (tpFIR,
	// shared with the LUFS true-peak meter), so the limiter constrains the
	// reconstructed waveform — the real peaks between samples — not just the
	// discrete sample values. That is what makes this a genuine true-peak limiter:
	// the output's true peak is guaranteed under the ceiling at the working rate,
	// no oversampled pipeline required to fake it. The per-channel envelopes are
	// folded straight into required[] rather than materialized — on multi-hour
	// files the two extra float64 arrays were gigabytes of transient memory —
	// and the scan is chunk-parallel: each output depends only on the 12
	// preceding (read-only) input samples and the writes are disjoint.
	required := make([]float64, n)
	chunkedParallel(n, func(start, end int) {
		requiredGainRange(left, right, required, thresh, start, end)
	})

	// winMin[i] = min(required[i .. i+look]); the gain therefore starts dropping
	// as soon as a peak enters the lookahead window, never after it has passed.
	winMin := slidingMin(required, look)

	relCoef := onePoleCoef(releaseMs, sampleRate)

	// Instant attack to the windowed minimum, smooth release. Because the gain
	// snaps straight to winMin (which already anticipates every peak inside the
	// lookahead) and only ever rises toward it on release, gain[i] <= winMin[i] <=
	// required[i] at all times — so the output true peak is GUARANTEED <= ceiling.
	// A one-pole attack would lag and overshoot; the lookahead (> FIR length) gives
	// the step room to settle before the peak emerges, keeping it click-free enough.
	gain := 1.0
	for i := range n {
		target := winMin[i]
		if target < gain {
			gain = target // instant attack — the conformance guarantee
		} else {
			gain = relCoef*gain + (1-relCoef)*target
		}
		left[i] = clampMag(left[i]*gain, thresh)
		right[i] = clampMag(right[i]*gain, thresh)
	}
}

// requiredGainRange fills required[start:end] with the gain that tames the
// stereo-linked true peak at each position to thresh (1.0 where no reduction
// is needed). The FIR reads input back to start-11; zero before index 0.
func requiredGainRange(left, right, required []float64, thresh float64, start, end int) {
	const taps = 12
	for i := start; i < end; i++ {
		var mx float64
		for phase := range 4 {
			var accL, accR float64
			for k := range taps {
				idx := i - k
				if idx < 0 {
					break
				}
				accL += tpFIR[phase][k] * left[idx]
				accR += tpFIR[phase][k] * right[idx]
			}
			if a := math.Abs(accL); a > mx {
				mx = a
			}
			if a := math.Abs(accR); a > mx {
				mx = a
			}
		}
		if mx > thresh {
			required[i] = thresh / mx
		} else {
			required[i] = 1
		}
	}
}

// chunkedParallel splits [0, n) into one contiguous range per worker and
// runs fn over them concurrently. fn must be safe for disjoint ranges (the
// envelope/peak scans here only read shared input and write disjoint output).
// Small inputs run inline.
func chunkedParallel(n int, fn func(start, end int)) {
	workers := runtime.GOMAXPROCS(0)
	if workers > 8 {
		workers = 8
	}
	const minChunk = 1 << 16
	if n < minChunk*2 || workers < 2 {
		fn(0, n)
		return
	}
	chunk := (n + workers - 1) / workers
	var wg sync.WaitGroup
	for w := range workers {
		start := w * chunk
		if start >= n {
			break
		}
		end := min(start+chunk, n)
		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			fn(start, end)
		}(start, end)
	}
	wg.Wait()
}

// slidingMin returns, for every index i, the minimum of x over the forward
// window [i, i+look]. It runs in O(n) via a monotonic deque of candidate
// indices whose values increase from the front (so the front is always the
// window minimum). Scanning right-to-left lets each index be pushed once and the
// out-of-window front be dropped once.
func slidingMin(x []float64, look int) []float64 {
	n := len(x)
	out := make([]float64, n)
	dq := make([]int, 0, look+1) // indices, x increasing from front to back
	for i := n - 1; i >= 0; i-- {
		for len(dq) > 0 && x[dq[len(dq)-1]] >= x[i] {
			dq = dq[:len(dq)-1]
		}
		dq = append(dq, i)
		for dq[0] > i+look {
			dq = dq[1:]
		}
		out[i] = x[dq[0]]
	}
	return out
}

// dsWindowSeconds is ffmpeg astats' default `length`: the time constant of the
// exponential moving average behind the windowed RMS-peak measurement.
const dsWindowSeconds = 0.05

// MeasureDynamicsScore computes the Dynamics Score for path natively in Go,
// without shelling out to ffmpeg. The score is a purely time-domain statistic
// (no FFT), so it needs nothing astats provides that we cannot compute directly.
//
// It reproduces astats' channel-1 figures: the overall RMS level, the crest
// factor (peak / RMS), and the RMS peak — the maximum over the signal of an
// exponential moving average of the squared samples, time constant
// dsWindowSeconds. That EMA matches astats' "RMS peak dB" to within ~0.001 dB
// across window lengths. The score is sqrt(crest) × (RMSpeak_dB − RMSlevel_dB),
// identical to ParseDynamicsScore so the native and ffmpeg paths agree.
func MeasureDynamicsScore(path string) (*DynamicsScoreAnalysis, error) {
	left, _, sampleRate, err := readStereo(path)
	if err != nil {
		return nil, fmt.Errorf("dynamics score: %w", err)
	}
	return dynamicsScore(left, float64(sampleRate)), nil
}

// MeasureDynamicsScoreSamples computes the Dynamics Score from in-memory
// channel-1 (left) samples — the buffer-level form of MeasureDynamicsScore for
// callers that keep the whole file resident across processing stages.
func MeasureDynamicsScoreSamples(left []float64, sampleRate float64) *DynamicsScoreAnalysis {
	return dynamicsScore(left, sampleRate)
}

// dynamicsScore is the in-memory core of MeasureDynamicsScore, operating on the
// channel-1 (left) samples — the channel astats' DS is parsed from.
func dynamicsScore(x []float64, sampleRate float64) *DynamicsScoreAnalysis {
	r := &DynamicsScoreAnalysis{}
	n := len(x)
	if n == 0 {
		return r
	}

	alpha := 1.0 - math.Exp(-1.0/(dsWindowSeconds*sampleRate))
	var sumSq, peak, ema, maxEMA float64
	for _, s := range x {
		sq := s * s
		sumSq += sq
		if a := math.Abs(s); a > peak {
			peak = a
		}
		ema += alpha * (sq - ema)
		if ema > maxEMA {
			maxEMA = ema
		}
	}

	rms := math.Sqrt(sumSq / float64(n))
	if rms <= 0 {
		return r // digital silence: leave the score at zero
	}
	r.RMSLevel = 20 * math.Log10(rms)
	r.RMSPeak = 10 * math.Log10(maxEMA) // 10·log10 — maxEMA is already power
	r.CrestFactor = peak / rms
	r.DynamicsScore = math.Sqrt(r.CrestFactor) * (r.RMSPeak - r.RMSLevel)
	return r
}

// PeakPercentile returns the pct-th percentile (0–100) of per-window peak levels
// across the file, in dBFS. Each window is windowSec long and its level is the
// max |sample| over both channels. pct=95 yields the level just under the loudest
// ~5% of windows — a natural place to put a limiter threshold that should catch
// only the highest peaks and leave everything else alone.
func PeakPercentile(path string, windowSec, pct float64) (float64, error) {
	left, right, sampleRate, err := readStereo(path)
	if err != nil {
		return 0, fmt.Errorf("peak percentile: %w", err)
	}
	n := min(len(left), len(right))
	if n == 0 {
		return -100, nil
	}
	win := int(windowSec * float64(sampleRate))
	if win < 1 {
		win = 1
	}
	peaks := make([]float64, 0, n/win+1)
	for i := 0; i < n; i += win {
		end := min(i+win, n)
		var pk float64
		for j := i; j < end; j++ {
			if a := math.Abs(left[j]); a > pk {
				pk = a
			}
			if a := math.Abs(right[j]); a > pk {
				pk = a
			}
		}
		peaks = append(peaks, pk)
	}
	sort.Float64s(peaks)
	return LinearToDb(percentile(peaks, pct)), nil
}

// limiterCalibration is the threshold/attack/release triple that drives the
// time-domain limiters at a given strength. These are the canonical pre-LUFS
// limiter values (they previously lived in the ffmpeg alimiter path, since
// replaced by the native conformity limiter).
//
// Calibrated for Apple AAC at bitrates 400 (no processing), 128 (strength 1),
// 96 (strength 2) and 64 kbit/s (strength 3); all land at -1.1...-1 dB TP. Other
// encoders and bitrates may differ.
type limiterCalibration struct {
	thresholdDb float64
	attackMs    float64
	releaseMs   float64
}

func softLimiterCalibration(strength int) limiterCalibration {
	c := limiterCalibration{thresholdDb: -3.2, attackMs: 20, releaseMs: 80}
	switch strength {
	case 1:
		c = limiterCalibration{thresholdDb: -3.2, attackMs: 18, releaseMs: 75}
	case 2:
		c = limiterCalibration{thresholdDb: -4.2, attackMs: 10, releaseMs: 60}
	case 3:
		c = limiterCalibration{thresholdDb: -10.0, attackMs: 8, releaseMs: 16}
	}
	return c
}

// characterLimiterKnee is the soft-knee width (dB) the strength-preset character
// limiter bends over; the conformity limiter is a hard brick wall and uses none.
const characterLimiterKnee = 6.0

// LimitConforming applies the lookahead brick-wall (conformity) limiter at the
// calibrated strength, rewriting path as a 32-bit float WAV. The calibration's
// attack time is used as the lookahead window.
func LimitConforming(path string, strength int) error {
	c := softLimiterCalibration(strength)
	left, right, sampleRate, err := readStereo(path)
	if err != nil {
		return fmt.Errorf("conformity limiter: %w", err)
	}
	LookaheadLimitSamples(left, right, float64(sampleRate), c.thresholdDb, c.attackMs, c.releaseMs)
	return WriteFloat32WAV(path, sampleRate, left, right)
}

// LimitCharacter applies the soft-knee character limiter at the calibrated
// strength, rewriting path as a 32-bit float WAV.
func LimitCharacter(path string, strength int) error {
	c := softLimiterCalibration(strength)
	left, right, sampleRate, err := readStereo(path)
	if err != nil {
		return fmt.Errorf("character limiter: %w", err)
	}
	CharacterLimitSamples(left, right, float64(sampleRate), c.thresholdDb, characterLimiterKnee, c.attackMs, c.releaseMs)
	return WriteFloat32WAV(path, sampleRate, left, right)
}

// CharacterLimiter applies a soft-knee limiter at path in place, rewriting it as
// a 32-bit float WAV. Unlike LookaheadLimiter it does not look ahead, so its
// finite attack lets transients punch through and its release pumps — that
// audible behaviour is the "character". The time factors are the caller's:
// attackMs / releaseMs shape how fast the gain dives and recovers, kneeDb sets
// how gently it bends into limiting around thresholdDb.
func CharacterLimiter(path string, thresholdDb, kneeDb, attackMs, releaseMs float64) error {
	left, right, sampleRate, err := readStereo(path)
	if err != nil {
		return fmt.Errorf("character limiter: %w", err)
	}
	CharacterLimitSamples(left, right, float64(sampleRate), thresholdDb, kneeDb, attackMs, releaseMs)
	return WriteFloat32WAV(path, sampleRate, left, right)
}

// CharacterLimitSamples is the in-place core of CharacterLimiter: a feed-forward
// limiter is just a compressor with an infinite ratio and no make-up gain.
func CharacterLimitSamples(left, right []float64, sampleRate, thresholdDb, kneeDb, attackMs, releaseMs float64) {
	processDynamics(left, right, sampleRate, thresholdDb, math.Inf(1), kneeDb, attackMs, releaseMs, 0, 0)
}

// Compress applies a feed-forward compressor at path in place, rewriting it as a
// 32-bit float WAV. All the controls are the caller's: thresholdDb, ratio (>= 1),
// kneeDb, the attackMs / releaseMs time factors and makeupDb of output gain. The
// level detector is peak when rmsMs <= 0, or RMS with that averaging window when
// rmsMs > 0 (use RMS for loudness/LRA leveling).
func Compress(path string, thresholdDb, ratio, kneeDb, attackMs, releaseMs, makeupDb, rmsMs float64) error {
	left, right, sampleRate, err := readStereo(path)
	if err != nil {
		return fmt.Errorf("compressor: %w", err)
	}
	CompressSamples(left, right, float64(sampleRate), thresholdDb, ratio, kneeDb, attackMs, releaseMs, makeupDb, rmsMs)
	return WriteFloat32WAV(path, sampleRate, left, right)
}

// CompressSamples is the in-place core of Compress.
func CompressSamples(left, right []float64, sampleRate, thresholdDb, ratio, kneeDb, attackMs, releaseMs, makeupDb, rmsMs float64) {
	processDynamics(left, right, sampleRate, thresholdDb, ratio, kneeDb, attackMs, releaseMs, makeupDb, rmsMs)
}

// upwardGainDb returns the boost (>= 0 dB) an upward compressor applies to a
// passage at levelDb sitting BELOW thresholdDb: (threshold - level)·(1 - 1/ratio),
// soft-kneed and capped at maxBoostDb so it can't lift the noise floor without
// bound. Levels at or above the threshold get 0. It is the mirror of staticGainDb.
func upwardGainDb(levelDb, thresholdDb, ratio, kneeDb, maxBoostDb float64) float64 {
	slope := 1.0 - 1.0/ratio // 0 at ratio 1, -> 1 as ratio grows
	under := thresholdDb - levelDb

	var boost float64
	switch {
	case kneeDb > 0 && 2*under < -kneeDb:
		return 0
	case kneeDb > 0 && 2*math.Abs(under) <= kneeDb:
		x := under + kneeDb/2
		boost = slope * x * x / (2 * kneeDb)
	default:
		if under <= 0 {
			return 0
		}
		boost = slope * under
	}
	if boost > maxBoostDb {
		boost = maxBoostDb
	}
	return boost
}

// UpwardCompress raises passages below thresholdDb toward it, shrinking loudness
// range from the bottom — the complement of the downward Compress, which lowers
// passages above threshold. Sharing the work between the two lets you control LRA
// with far less gain reduction on the loud side, so fewer downward-compression
// artefacts. maxBoostDb caps the lift so quiet gaps / noise floor aren't dragged
// up indefinitely; detector and timing are as in Compress (use rmsMs>0 for
// loudness leveling). The attack is the rate the boost ramps IN, so a slow attack
// keeps brief gaps from being lifted.
func UpwardCompress(path string, thresholdDb, ratio, kneeDb, attackMs, releaseMs, maxBoostDb, rmsMs float64) error {
	left, right, sampleRate, err := readStereo(path)
	if err != nil {
		return fmt.Errorf("upward compressor: %w", err)
	}
	UpwardCompressSamples(left, right, float64(sampleRate), thresholdDb, ratio, kneeDb, attackMs, releaseMs, maxBoostDb, rmsMs)
	return WriteFloat32WAV(path, sampleRate, left, right)
}

// UpwardCompressSamples is the in-place core of UpwardCompress.
func UpwardCompressSamples(left, right []float64, sampleRate, thresholdDb, ratio, kneeDb, attackMs, releaseMs, maxBoostDb, rmsMs float64) {
	n := min(len(left), len(right))
	if n == 0 {
		return
	}
	attCoef := onePoleCoef(attackMs, sampleRate)
	relCoef := onePoleCoef(releaseMs, sampleRate)
	rmsCoef := onePoleCoef(rmsMs, sampleRate)
	useRMS := rmsMs > 0
	var meanSq float64

	gain := 1.0 // >= 1: this stage only boosts
	for i := range n {
		var level float64
		if useRMS {
			power := 0.5 * (left[i]*left[i] + right[i]*right[i])
			meanSq = rmsCoef*meanSq + (1-rmsCoef)*power
			level = math.Sqrt(meanSq)
		} else {
			level = math.Max(math.Abs(left[i]), math.Abs(right[i]))
		}
		target := DbToLinear(upwardGainDb(LinearToDb(level), thresholdDb, ratio, kneeDb, maxBoostDb)) // >= 1

		if target > gain {
			gain = attCoef*gain + (1-attCoef)*target // boost ramping in (attack)
		} else {
			gain = relCoef*gain + (1-relCoef)*target // backing off (release)
		}

		left[i] *= gain
		right[i] *= gain
	}
}

// processDynamics is the shared feed-forward compressor/limiter engine. For each
// frame it derives a stereo-linked level, looks up the static gain reduction on
// the soft-knee curve, then smooths the gain with the attack coefficient when
// more reduction is wanted and the release coefficient when less is wanted (a
// decoupled detector — branch on the gain direction, not on the level). The
// smoothed gain plus a constant make-up gain is applied to both channels.
//
// The level detector is peak (instantaneous max|L|,|R|) when rmsMs <= 0 — correct
// for limiting — or RMS when rmsMs > 0: a one-pole-smoothed mean-square with that
// time constant, which tracks sustained loudness rather than transient peaks and
// is the right detector for loudness/LRA leveling.
func processDynamics(left, right []float64, sampleRate, thresholdDb, ratio, kneeDb, attackMs, releaseMs, makeupDb, rmsMs float64) {
	n := min(len(left), len(right))
	if n == 0 {
		return
	}
	attCoef := onePoleCoef(attackMs, sampleRate)
	relCoef := onePoleCoef(releaseMs, sampleRate)
	makeup := DbToLinear(makeupDb)

	rmsCoef := onePoleCoef(rmsMs, sampleRate)
	useRMS := rmsMs > 0
	var meanSq float64 // smoothed mean-square for the RMS detector

	gain := 1.0
	for i := range n {
		var level float64
		if useRMS {
			power := 0.5 * (left[i]*left[i] + right[i]*right[i])
			meanSq = rmsCoef*meanSq + (1-rmsCoef)*power
			level = math.Sqrt(meanSq)
		} else {
			level = math.Max(math.Abs(left[i]), math.Abs(right[i]))
		}
		reductionDb := staticGainDb(LinearToDb(level), thresholdDb, ratio, kneeDb)
		target := DbToLinear(reductionDb) // <= 1

		if target < gain {
			gain = attCoef*gain + (1-attCoef)*target
		} else {
			gain = relCoef*gain + (1-relCoef)*target
		}

		left[i] *= gain * makeup
		right[i] *= gain * makeup
	}
}

// readStereo reads a WAV at path into stereo float64 buffers and also returns
// its sample rate. It is a thin alias for the package-central readSamples,
// kept for the time-based processors here that turn millisecond time factors
// into per-sample coefficients.
func readStereo(path string) (left, right []float64, sampleRate uint32, err error) {
	return readSamples(path)
}
