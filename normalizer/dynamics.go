package normalizer

// Streaming time-domain dynamics — the podcast loudness/dynamics chain that runs
// in the podcore transcoder without ever holding the whole file in memory.
//
// Every processor here is a faithful streaming port of the in-memory cores in
// the TNT desktop package (github.com/fremen-fi/tnt go/audio): same per-sample
// math, fed from an io.Reader instead of a resident []float64. The processors
// are all causal one-pole followers except the conformance limiter, which uses
// a bounded lookahead ring buffer — so memory is O(lookahead), a few tens of KiB
// regardless of file length. ffmpeg is only ever used by the caller to decode
// the source into this stream and to AAC-encode what comes out; it performs no
// loudness or dynamics work.
//
// The chain the podcore transcoder runs:
//
//	measure → (downward) Compressor → Character Limiter → gain to target LUFS →
//	Conformance Limiter (true-peak brick wall)
//
// The Compressor and Character Limiter share one feed-forward engine (the limiter
// is a compressor with an infinite ratio). The Conformance Limiter is a genuine
// true-peak limiter: its detection runs the BS.1770 4× polyphase FIR, so the
// reconstructed inter-sample peak is guaranteed under the ceiling — no oversampled
// ffmpeg alimiter needed.

import (
	"io"
	"math"
)

// ── dB / smoothing helpers (ported verbatim from go/audio) ──────────────────

// dbToLin converts decibels to linear amplitude.
func dbToLin(db float64) float64 { return math.Pow(10, db/20) }

// linToDb converts linear amplitude to decibels; non-positive input floors at
// -100 dB (matching go/audio.LinearToDb — the dynamics math depends on this
// floor, NOT on -Inf, so silent samples produce a finite level).
func linToDb(lin float64) float64 {
	if lin <= 0 {
		return -100.0
	}
	return 20 * math.Log10(lin)
}

// onePoleCoef returns the one-pole smoothing coefficient for a timeMs time
// constant at sampleRate. A non-positive timeMs yields 0 (instantaneous move).
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

func clampf(x, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, x)) }

// staticGainDb returns the gain (dB, <= 0) the static soft-knee transfer curve
// applies to an input at levelDb, given threshold, ratio and a knee kneeDb wide
// centred on the threshold. ratio == +Inf makes it a hard limiter. Ported
// verbatim from go/audio.staticGainDb (Reiss & McPherson soft knee).
func staticGainDb(levelDb, thresholdDb, ratio, kneeDb float64) float64 {
	slope := 1.0/ratio - 1.0
	over := levelDb - thresholdDb
	switch {
	case kneeDb > 0 && 2*over < -kneeDb:
		return 0
	case kneeDb > 0 && 2*math.Abs(over) <= kneeDb:
		x := over + kneeDb/2
		return slope * x * x / (2 * kneeDb)
	default:
		if over <= 0 {
			return 0
		}
		return slope * over
	}
}

// tpFIR is the order-48, 4-phase polyphase FIR from ITU-R BS.1770-5 Annex 2
// (4× over-sampling for true-peak estimation). Ported verbatim from go/audio.
// Output = sum over k of tpFIR[phase][k]·x[n-k]; tap 0 is the most recent sample.
var tpFIR = [4][12]float64{
	{
		0.0017089843750, 0.0109863281250, -0.0196533203125, 0.0332031250000,
		-0.0594482421875, 0.1373291015625, 0.9721679687500, -0.1022949218750,
		0.0476074218750, -0.0266113281250, 0.0148925781250, -0.0083007812500,
	},
	{
		-0.0291748046875, 0.0292968750000, -0.0517578125000, 0.0891113281250,
		-0.1665039062500, 0.4650878906250, 0.7797851562500, -0.2003173828125,
		0.1015625000000, -0.0582275390625, 0.0330810546875, -0.0189208984375,
	},
	{
		-0.0189208984375, 0.0330810546875, -0.0582275390625, 0.1015625000000,
		-0.2003173828125, 0.7797851562500, 0.4650878906250, -0.1665039062500,
		0.0891113281250, -0.0517578125000, 0.0292968750000, -0.0291748046875,
	},
	{
		-0.0083007812500, 0.0148925781250, -0.0266113281250, 0.0476074218750,
		-0.1022949218750, 0.9721679687500, 0.1373291015625, -0.0594482421875,
		0.0332031250000, -0.0196533203125, 0.0109863281250, 0.0017089843750,
	},
}

// tpHist is a 12-sample ring of one channel's recent input, the history the
// true-peak FIR reads back over. Samples older than the start of stream (or than
// 12 ago) read as absent.
type tpHist struct {
	buf   [12]float64
	count int
}

func (h *tpHist) push(x float64) {
	h.buf[h.count%12] = x
	h.count++
}

// at returns the sample k positions back from the most recent (k==0 is current),
// and false if that sample predates the stream.
func (h *tpHist) at(k int) (float64, bool) {
	idx := h.count - 1 - k
	if idx < 0 {
		return 0, false
	}
	return h.buf[idx%12], true
}

// interpMax returns the maximum absolute inter-sample (true) peak at the current
// position across both channels — max over the 4 polyphase sub-samples of
// max(|FIR(L)|, |FIR(R)|). Stereo-linked, matching go/audio.requiredGainRange.
func interpMax(hL, hR *tpHist) float64 {
	var mx float64
	for phase := 0; phase < 4; phase++ {
		var accL, accR float64
		for k := 0; k < 12; k++ {
			l, ok := hL.at(k)
			if !ok {
				break
			}
			r, _ := hR.at(k)
			accL += tpFIR[phase][k] * l
			accR += tpFIR[phase][k] * r
		}
		if a := math.Abs(accL); a > mx {
			mx = a
		}
		if a := math.Abs(accR); a > mx {
			mx = a
		}
	}
	return mx
}

// ── Compressor / Character Limiter (shared feed-forward engine) ─────────────

// compressor is the streaming form of go/audio.processDynamics: a feed-forward
// compressor (finite ratio) or limiter (ratio == +Inf). Detection is peak
// (rmsMs <= 0) or one-pole-smoothed RMS (rmsMs > 0); the gain attacks when more
// reduction is wanted and releases when less is. Stereo-linked. O(1) state.
type compressor struct {
	attCoef, relCoef float64
	rmsCoef          float64
	useRMS           bool
	makeup           float64
	thresholdDb      float64
	ratio            float64
	kneeDb           float64

	meanSq float64
	gain   float64
}

func newCompressor(sampleRate, thresholdDb, ratio, kneeDb, attackMs, releaseMs, makeupDb, rmsMs float64) *compressor {
	return &compressor{
		attCoef:     onePoleCoef(attackMs, sampleRate),
		relCoef:     onePoleCoef(releaseMs, sampleRate),
		rmsCoef:     onePoleCoef(rmsMs, sampleRate),
		useRMS:      rmsMs > 0,
		makeup:      dbToLin(makeupDb),
		thresholdDb: thresholdDb,
		ratio:       ratio,
		kneeDb:      kneeDb,
		gain:        1.0,
	}
}

func (c *compressor) step(l, r float64) (float64, float64) {
	var level float64
	if c.useRMS {
		power := 0.5 * (l*l + r*r)
		c.meanSq = c.rmsCoef*c.meanSq + (1-c.rmsCoef)*power
		level = math.Sqrt(c.meanSq)
	} else {
		level = math.Max(math.Abs(l), math.Abs(r))
	}
	target := dbToLin(staticGainDb(linToDb(level), c.thresholdDb, c.ratio, c.kneeDb))
	if target < c.gain {
		c.gain = c.attCoef*c.gain + (1-c.attCoef)*target
	} else {
		c.gain = c.relCoef*c.gain + (1-c.relCoef)*target
	}
	return l * c.gain * c.makeup, r * c.gain * c.makeup
}

// upwardGainDb returns the boost (>= 0 dB) an upward compressor applies to a
// passage at levelDb sitting BELOW thresholdDb: (threshold - level)·(1 - 1/ratio),
// soft-kneed and capped at maxBoostDb. Levels at or above the threshold get 0.
// The mirror of staticGainDb. Ported verbatim from go/audio.upwardGainDb.
func upwardGainDb(levelDb, thresholdDb, ratio, kneeDb, maxBoostDb float64) float64 {
	slope := 1.0 - 1.0/ratio
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

// upwardCompressor is the streaming form of go/audio.UpwardCompressSamples: it
// lifts passages below thresholdDb toward it, shrinking the loudness range from
// the bottom — the complement of the downward compressor, which lowers passages
// above threshold. Sharing the LRA reduction between the two needs far less gain
// reduction on the loud side, so fewer downward-compression artefacts and a much
// larger total LRA reduction than downward alone. Boost-only (gain >= 1), capped
// at maxBoostDb so quiet gaps / noise floor aren't dragged up without bound.
// O(1) state.
type upwardCompressor struct {
	attCoef, relCoef float64
	rmsCoef          float64
	useRMS           bool
	thresholdDb      float64
	ratio            float64
	kneeDb           float64
	maxBoostDb       float64

	meanSq float64
	gain   float64
}

func newUpwardCompressor(sampleRate, thresholdDb, ratio, kneeDb, attackMs, releaseMs, maxBoostDb, rmsMs float64) *upwardCompressor {
	return &upwardCompressor{
		attCoef:     onePoleCoef(attackMs, sampleRate),
		relCoef:     onePoleCoef(releaseMs, sampleRate),
		rmsCoef:     onePoleCoef(rmsMs, sampleRate),
		useRMS:      rmsMs > 0,
		thresholdDb: thresholdDb,
		ratio:       ratio,
		kneeDb:      kneeDb,
		maxBoostDb:  maxBoostDb,
		gain:        1.0,
	}
}

func (u *upwardCompressor) step(l, r float64) (float64, float64) {
	var level float64
	if u.useRMS {
		power := 0.5 * (l*l + r*r)
		u.meanSq = u.rmsCoef*u.meanSq + (1-u.rmsCoef)*power
		level = math.Sqrt(u.meanSq)
	} else {
		level = math.Max(math.Abs(l), math.Abs(r))
	}
	target := dbToLin(upwardGainDb(linToDb(level), u.thresholdDb, u.ratio, u.kneeDb, u.maxBoostDb)) // >= 1
	if target > u.gain {
		u.gain = u.attCoef*u.gain + (1-u.attCoef)*target // boost ramping in (attack)
	} else {
		u.gain = u.relCoef*u.gain + (1-u.relCoef)*target // backing off (release)
	}
	return l * u.gain, r * u.gain
}

// ── Conformance limiter (streaming true-peak brick wall) ────────────────────

// conformanceLimiter is the streaming form of go/audio.LookaheadLimitSamples.
// For each sample it computes the gain needed to tame the true (inter-sample)
// peak to the ceiling, takes a sliding-window minimum over the lookahead window
// (so the gain is already down before a peak emerges — the conformance
// guarantee), smooths with instant attack / one-pole release, applies it and
// clamps at the ceiling as a brick-wall backstop. Output is delayed by `look`
// samples; Flush emits the tail. Memory is O(look).
type conformanceLimiter struct {
	thresh  float64
	look    int
	relCoef float64

	histL, histR tpHist

	pendL, pendR []float64 // frames pushed but not yet emitted (FIFO)
	dqIdx        []int     // monotonic deque: indices, values increasing from front
	dqVal        []float64

	gain    float64
	inIdx   int
	emitIdx int
}

func newConformanceLimiter(sampleRate, thresholdDb, lookaheadMs, releaseMs float64) *conformanceLimiter {
	look := int(math.Round(lookaheadMs / 1000.0 * sampleRate))
	if look < 1 {
		look = 1
	}
	return &conformanceLimiter{
		thresh:  dbToLin(thresholdDb),
		look:    look,
		relCoef: onePoleCoef(releaseMs, sampleRate),
		gain:    1.0,
	}
}

// push feeds one input frame and returns one output frame once the lookahead
// window has filled (ok == false during the initial `look`-sample warm-up).
func (c *conformanceLimiter) push(l, r float64) (outL, outR float64, ok bool) {
	i := c.inIdx
	c.histL.push(l)
	c.histR.push(r)
	mx := interpMax(&c.histL, &c.histR)
	req := 1.0
	if mx > c.thresh {
		req = c.thresh / mx
	}

	c.pendL = append(c.pendL, l)
	c.pendR = append(c.pendR, r)
	for len(c.dqVal) > 0 && c.dqVal[len(c.dqVal)-1] >= req {
		c.dqVal = c.dqVal[:len(c.dqVal)-1]
		c.dqIdx = c.dqIdx[:len(c.dqIdx)-1]
	}
	c.dqVal = append(c.dqVal, req)
	c.dqIdx = append(c.dqIdx, i)
	c.inIdx++

	if i-c.emitIdx >= c.look {
		outL, outR = c.emitFront()
		return outL, outR, true
	}
	return 0, 0, false
}

// emitFront emits the oldest buffered frame using the windowed-minimum gain.
func (c *conformanceLimiter) emitFront() (float64, float64) {
	winMin := c.dqVal[0]
	if winMin < c.gain {
		c.gain = winMin // instant attack — the conformance guarantee
	} else {
		c.gain = c.relCoef*c.gain + (1-c.relCoef)*winMin
	}
	oL := clampMag(c.pendL[0]*c.gain, c.thresh)
	oR := clampMag(c.pendR[0]*c.gain, c.thresh)
	c.pendL = c.pendL[1:]
	c.pendR = c.pendR[1:]
	if c.dqIdx[0] == c.emitIdx {
		c.dqVal = c.dqVal[1:]
		c.dqIdx = c.dqIdx[1:]
	}
	c.emitIdx++
	return oL, oR
}

// flush emits the remaining buffered frames (their lookahead windows truncate at
// end-of-stream), calling emit for each.
func (c *conformanceLimiter) flush(emit func(l, r float64)) {
	for c.emitIdx < c.inIdx {
		for len(c.dqIdx) > 0 && c.dqIdx[0] < c.emitIdx {
			c.dqVal = c.dqVal[1:]
			c.dqIdx = c.dqIdx[1:]
		}
		oL, oR := c.emitFront()
		emit(oL, oR)
	}
}

// ── Measurement (single streaming pass) ─────────────────────────────────────

// ChainMeasurement carries everything the chain needs to set its parameters: the
// EBU R128 figures plus the Dynamics-Score statistics (go/audio convention).
type ChainMeasurement struct {
	IntegratedLUFS float64 // BS.1770 integrated loudness (channel-summed), -Inf for silence
	LRA            float64 // EBU R128 loudness range (LU)
	TruePeakDB     float64 // inter-sample true peak (dBTP), -Inf for silence
	RMSLevelDB     float64 // overall RMS level of channel 1 (dB)
	RMSPeakDB      float64 // windowed RMS-peak of channel 1 (dB)
	CrestFactor    float64 // peak / RMS of channel 1
	DynamicsScore  float64 // sqrt(crest)·(RMSpeak−RMSlevel)
}

const dsWindowSeconds = 0.05 // DS RMS-peak EMA time constant (astats default)

type measurer struct {
	filters [2]channelFilter
	step    int
	stStep  int

	stepSumSqL, stepSumSqR float64
	stepN                  int
	stSumSqL, stSumSqR     float64
	stN                    int
	stepE, stStepE         []float64

	tpL, tpR tpHist
	maxTP    float64

	dsAlpha                        float64
	dsSumSq, dsPeak, dsEMA, dsMaxE float64
	dsN                            int
}

func newMeasurer(sampleRate int) *measurer {
	fs := float64(sampleRate)
	return &measurer{
		filters: newKweightFilters(fs),
		step:    sampleRate / 10,
		stStep:  sampleRate,
		stepE:   make([]float64, 0, 4096),
		stStepE: make([]float64, 0, 4096),
		dsAlpha: 1.0 - math.Exp(-1.0/(dsWindowSeconds*fs)),
	}
}

func (m *measurer) add(l, r float64) {
	// K-weighted, channel-summed block energies for LUFS / LRA (BS.1770).
	kl := m.filters[0].process(l)
	kr := m.filters[1].process(r)
	klSq, krSq := kl*kl, kr*kr
	m.stepSumSqL += klSq
	m.stepSumSqR += krSq
	m.stepN++
	m.stSumSqL += klSq
	m.stSumSqR += krSq
	m.stN++
	if m.stepN >= m.step {
		m.stepE = append(m.stepE, (m.stepSumSqL+m.stepSumSqR)/float64(m.stepN))
		m.stepSumSqL, m.stepSumSqR, m.stepN = 0, 0, 0
	}
	if m.stN >= m.stStep {
		m.stStepE = append(m.stStepE, (m.stSumSqL+m.stSumSqR)/float64(m.stN))
		m.stSumSqL, m.stSumSqR, m.stN = 0, 0, 0
	}

	// True peak across both channels.
	m.tpL.push(l)
	m.tpR.push(r)
	if mx := interpMax(&m.tpL, &m.tpR); mx > m.maxTP {
		m.maxTP = mx
	}

	// Dynamics Score on channel 1 (left).
	sq := l * l
	m.dsSumSq += sq
	if a := math.Abs(l); a > m.dsPeak {
		m.dsPeak = a
	}
	m.dsEMA += m.dsAlpha * (sq - m.dsEMA)
	if m.dsEMA > m.dsMaxE {
		m.dsMaxE = m.dsEMA
	}
	m.dsN++
}

func (m *measurer) result() ChainMeasurement {
	stepE := m.stepE
	stStepE := m.stStepE
	if m.stepN > 0 {
		stepE = append(stepE, (m.stepSumSqL+m.stepSumSqR)/float64(m.stepN))
	}
	if m.stN > 0 {
		stStepE = append(stStepE, (m.stSumSqL+m.stSumSqR)/float64(m.stN))
	}

	res := ChainMeasurement{
		IntegratedLUFS: integratedLoudness(stepE, 4), // 400 ms block / 100 ms step
		LRA:            loudnessRange(stStepE, 3),    // 3 s block / 1 s step
		TruePeakDB:     math.Inf(-1),
		RMSLevelDB:     -100,
		RMSPeakDB:      -100,
	}
	if m.maxTP > 0 {
		res.TruePeakDB = 20 * math.Log10(m.maxTP)
	}
	if m.dsN > 0 {
		rms := math.Sqrt(m.dsSumSq / float64(m.dsN))
		if rms > 0 {
			res.RMSLevelDB = 20 * math.Log10(rms)
			if m.dsMaxE > 0 {
				res.RMSPeakDB = 10 * math.Log10(m.dsMaxE)
			}
			res.CrestFactor = m.dsPeak / rms
			res.DynamicsScore = math.Sqrt(res.CrestFactor) * (res.RMSPeakDB - res.RMSLevelDB)
		}
	}
	return res
}

// ── Chain parameters & staged drivers ───────────────────────────────────────

// StageParams holds one gentle compressor→upward→character stage's settings
// (sample-rate-independent) — the streaming equivalent of one go/audio
// reduceLRAPass. The converging multi-pass re-derives these each pass from the
// freshly re-measured signal, so the thresholds track the flattening signal and
// the stage auto-tapers toward the target.
type StageParams struct {
	CompThresholdDb float64
	CompRatio       float64
	CompKneeDb      float64
	CompAttackMs    float64
	CompReleaseMs   float64
	CompRMSWindowMs float64

	UpThresholdDb float64
	UpRatio       float64
	UpKneeDb      float64
	UpAttackMs    float64
	UpReleaseMs   float64
	UpMaxBoostDb  float64
	UpRMSWindowMs float64

	CharThresholdDb float64
	CharKneeDb      float64
	CharAttackMs    float64
	CharReleaseMs   float64
}

// compModifiers reproduces go/audio.GetCompressionModifiers: DS-driven attack /
// release / ratio multipliers.
func compModifiers(ds float64) (attack, release, ratioMul float64) {
	switch {
	case ds < 9.0:
		return 4.0, 4.0, 0.15
	case ds < 15.0:
		return 2.0, 2.0, 2.1
	case ds <= 21.0:
		return 1.0, 1.0, 1.0
	default:
		excess := math.Min(ds-21.0, 79.0)
		scale := 2.0 + (2.0 * excess / 79.0)
		return 1.0 / scale, 1.0 / scale, 4.0 + (4.0 * excess / 79.0)
	}
}

// DeriveStage sizes ONE compressor→upward→character stage from a measurement,
// mirroring go/audio.reduceLRAPass. The downward compressor lowers loud passages
// (threshold between the RMS level and peak, ratio sized to the overshoot); the
// upward compressor lifts quiet ones toward the RMS level, so the pair shrinks
// LRA from both ends with far less gain reduction than downward alone; the slow
// character limiter catches the residual. The converging loop calls this fresh
// on each re-measured pass, so it adapts and tapers toward the target.
func DeriveStage(m ChainMeasurement, targetLRA float64) StageParams {
	const wideKnee = 12.0
	const rmsWindowMs = 300.0
	const charKnee = 6.0

	over := m.LRA - targetLRA
	if over < 0 {
		over = 0
	}
	att, rel, _ := compModifiers(m.DynamicsScore)
	span := m.RMSPeakDB - m.RMSLevelDB
	dyn := clampf((m.DynamicsScore-9)/12, 0, 1)
	interp := clampf(0.6-over*0.05, 0.1, 0.6)

	return StageParams{
		CompThresholdDb: m.RMSLevelDB + interp*span,
		CompRatio:       clampf(1+over*0.15, 1, 6),
		CompKneeDb:      wideKnee,
		CompAttackMs:    100 * att,
		CompReleaseMs:   1500 * rel,
		CompRMSWindowMs: rmsWindowMs,

		UpThresholdDb: m.RMSLevelDB,
		UpRatio:       clampf(1+over*0.1, 1, 4),
		UpKneeDb:      wideKnee,
		UpAttackMs:    300 * att,
		UpReleaseMs:   600 * rel,
		UpMaxBoostDb:  5.0,
		UpRMSWindowMs: rmsWindowMs,

		CharThresholdDb: m.RMSPeakDB - (0.2+0.3*dyn)*span,
		CharKneeDb:      charKnee,
		CharAttackMs:    150 * att,
		CharReleaseMs:   1200 * rel,
	}
}

func (sp StageParams) build(sampleRate int) (comp *compressor, up *upwardCompressor, char *compressor) {
	fs := float64(sampleRate)
	comp = newCompressor(fs, sp.CompThresholdDb, sp.CompRatio, sp.CompKneeDb,
		sp.CompAttackMs, sp.CompReleaseMs, 0, sp.CompRMSWindowMs)
	up = newUpwardCompressor(fs, sp.UpThresholdDb, sp.UpRatio, sp.UpKneeDb,
		sp.UpAttackMs, sp.UpReleaseMs, sp.UpMaxBoostDb, sp.UpRMSWindowMs)
	// Character limiter: infinite-ratio soft-knee, peak detector.
	char = newCompressor(fs, sp.CharThresholdDb, math.Inf(1), sp.CharKneeDb,
		sp.CharAttackMs, sp.CharReleaseMs, 0, 0)
	return comp, up, char
}

// streamFrames reads interleaved f32 stereo from src, applies fn to each frame,
// and writes the result to dst. For zero-latency per-frame processors.
func streamFrames(src io.Reader, dst io.Writer, fn func(l, r float64) (float64, float64)) error {
	buf := make([]float32, frameChunk*2)
	for {
		n, rerr := readF32LE(src, buf)
		for i := 0; i+1 < n; i += 2 {
			l, r := fn(float64(buf[i]), float64(buf[i+1]))
			buf[i], buf[i+1] = float32(l), float32(r)
		}
		if n > 0 {
			if werr := writeF32LE(dst, buf[:n]); werr != nil {
				return werr
			}
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// ApplyStage streams src→dst applying one compressor→upward→character stage.
// The converging multi-pass calls this once per pass, re-deriving sp from the
// freshly measured intermediate each time.
func ApplyStage(src io.Reader, dst io.Writer, sampleRate int, sp StageParams) error {
	comp, up, char := sp.build(sampleRate)
	return streamFrames(src, dst, func(l, r float64) (float64, float64) {
		l, r = comp.step(l, r)
		l, r = up.step(l, r)
		l, r = char.step(l, r)
		return l, r
	})
}

// Conform streams src→dst applying a constant gainDB then the true-peak
// conformance limiter at ceilingDB — the final pass after loudness/LRA
// processing. lookaheadMs / releaseMs shape the limiter; memory is O(lookahead).
func Conform(src io.Reader, dst io.Writer, sampleRate int, gainDB, ceilingDB, lookaheadMs, releaseMs float64) error {
	gain := dbToLin(gainDB)
	lim := newConformanceLimiter(float64(sampleRate), ceilingDB, lookaheadMs, releaseMs)

	in := make([]float32, frameChunk*2)
	out := make([]float32, 0, frameChunk*2)
	flushOut := func() error {
		if len(out) == 0 {
			return nil
		}
		if err := writeF32LE(dst, out); err != nil {
			return err
		}
		out = out[:0]
		return nil
	}
	for {
		n, rerr := readF32LE(src, in)
		for i := 0; i+1 < n; i += 2 {
			l := float64(in[i]) * gain
			r := float64(in[i+1]) * gain
			if ol, or, ok := lim.push(l, r); ok {
				out = append(out, float32(ol), float32(or))
				if len(out) >= cap(out) {
					if err := flushOut(); err != nil {
						return err
					}
				}
			}
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	var flushErr error
	lim.flush(func(l, r float64) {
		out = append(out, float32(l), float32(r))
		if len(out) >= cap(out) {
			if err := flushOut(); err != nil && flushErr == nil {
				flushErr = err
			}
		}
	})
	if flushErr != nil {
		return flushErr
	}
	return flushOut()
}

// ── Streaming drivers ───────────────────────────────────────────────────────

const frameChunk = 8192 // stereo frames processed per read

// MeasureChain measures src (interleaved f32 stereo PCM) in a single streaming
// pass. Read to EOF; src need not be seekable.
func MeasureChain(src io.Reader, sampleRate int) (ChainMeasurement, error) {
	m := newMeasurer(sampleRate)
	buf := make([]float32, frameChunk*2)
	for {
		n, err := readF32LE(src, buf)
		for i := 0; i+1 < n; i += 2 {
			m.add(float64(buf[i]), float64(buf[i+1]))
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return ChainMeasurement{}, err
		}
	}
	return m.result(), nil
}
