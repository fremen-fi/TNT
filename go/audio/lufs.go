package audio

import (
	"fmt"
	"io"
	"math"
	"os"
	"sort"
)

// biquadFilter is a single Direct-Form-I biquad IIR section.
// Transfer function: H(z) = (b0 + b1·z⁻¹ + b2·z⁻²) / (1 + a1·z⁻¹ + a2·z⁻²)
//
// The pointer receiver on process() is essential: x1, x2, y1, y2 are the
// delay-line state. A value receiver would copy the struct on every call and
// discard the updated state, producing nonsense output.
type biquadFilter struct {
	b0, b1, b2 float64
	a1, a2     float64
	x1, x2     float64 // input delay line
	y1, y2     float64 // output delay line
}

func (f *biquadFilter) process(x float64) float64 {
	y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
	f.x2, f.x1 = f.x1, x
	f.y2, f.y1 = f.y1, y
	return y
}

// kWeightingFilters returns the two-stage K-weighting filter for sampleRate.
// K-weighting is defined in ITU-R BS.1770-4 Annex 1; coefficients are derived
// via the bilinear transform from the analog prototype, so any sample rate works.
//
// Named return values here are purely documentary — they tell the caller what
// they're getting without needing to read the function body.
func kWeightingFilters(sampleRate float64) (stage1, stage2 biquadFilter) {
	// Bare {} blocks let us reuse short names (K, denom) for each stage
	// without polluting the outer scope or adding ugly suffixes.

	// Stage 1: high-shelf pre-filter (+4 dB shelf at ~1.7 kHz).
	// Models the acoustic effect of the listener's head on perceived loudness.
	{
		const (
			f0     = 1681.974450955533
			Q      = 0.7071752369554196
			dbGain = 3.999843853973347
		)
		Vh := math.Pow(10, dbGain/20) // linear amplitude gain
		Vb := math.Pow(10, dbGain/40) // geometric midpoint = sqrt(Vh)
		K := math.Tan(math.Pi * f0 / sampleRate)
		denom := 1 + K/Q + K*K
		stage1 = biquadFilter{
			b0: (Vh + Vb*K/Q + K*K) / denom,
			b1: 2 * (K*K - Vh) / denom,
			b2: (Vh - Vb*K/Q + K*K) / denom,
			a1: 2 * (K*K - 1) / denom,
			a2: (1 - K/Q + K*K) / denom,
		}
	}

	// Stage 2: high-pass filter (RLB weighting, –3 dB at ~38 Hz).
	// De-emphasises very low frequencies that contribute to loudness measurement
	// but are rarely audible on consumer playback systems.
	{
		const (
			f0 = 38.13547087602444
			Q  = 0.5003270373238773
		)
		K := math.Tan(math.Pi * f0 / sampleRate)
		denom := 1 + K/Q + K*K
		stage2 = biquadFilter{
			b0: 1 / denom,
			b1: -2 / denom,
			b2: 1 / denom,
			a1: 2 * (K*K - 1) / denom,
			a2: (1 - K/Q + K*K) / denom,
		}
	}

	return
}

// kWeightChannel applies the two-stage K-weighting filter to a mono channel.
func kWeightChannel(samples []float64, sampleRate float64) []float64 {
	s1, s2 := kWeightingFilters(sampleRate)
	out := make([]float64, len(samples))
	for i, x := range samples {
		out[i] = s2.process(s1.process(x))
	}
	return out
}

// LUFSResult holds integrated loudness, true-peak and loudness-range,
// per ITU-R BS.1770-5 and EBU Tech 3342.
type LUFSResult struct {
	Integrated float64 // LUFS; math.Inf(-1) if the file contains only silence
	TruePeak   float64 // dBTP (highest across channels); math.Inf(-1) for silence
	LRA        float64 // Loudness Range in LU; 0 if fewer than two gated blocks
}

const (
	blockSizeSec = 0.4   // 400 ms measurement window
	hopSizeSec   = 0.1   // 100 ms hop → 75% overlap between windows
	absGateLevel = -70.0 // absolute gating threshold (LUFS)

	// LRA short-term parameters (EBU Tech 3342).
	lraBlockSizeSec = 3.0   // 3 s short-term window
	lraHopSizeSec   = 0.75  // 750 ms hop → 75% overlap
	lraRelGate      = -20.0 // relative gate is −20 LU below the ungated mean
)

// MeasureLUFS reads a WAV file and returns integrated loudness in LUFS.
func MeasureLUFS(path string) (*LUFSResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	h, err := readWAVHeader(f)
	if err != nil {
		return nil, fmt.Errorf("reading WAV header: %w", err)
	}

	raw := make([]byte, h.dataSize)
	if _, err := io.ReadFull(f, raw); err != nil {
		return nil, fmt.Errorf("reading PCM data: %w", err)
	}

	var left, right []float64
	switch {
	case h.audioFormat == 3 && h.bitsPerSample == 32:
		left, right = SamplesFromFloat32(raw)
	case h.audioFormat == 1 && h.bitsPerSample == 24:
		left, right = SamplesFromInt24(raw)
	case h.audioFormat == 1 && h.bitsPerSample == 32:
		left, right = SamplesFromInt32(raw)
	case h.audioFormat == 1 && h.bitsPerSample == 16:
		left, right = SamplesFromInt16(raw)
	default:
		return nil, fmt.Errorf("unsupported format: audioformat=%d bits=%d", h.audioFormat, h.bitsPerSample)
	}

	sr := float64(h.sampleRate)
	lufs := integratedLUFS(left, right, sr)
	tp := truePeak(left, right)
	lra := loudnessRange(left, right, sr)
	return &LUFSResult{Integrated: lufs, TruePeak: tp, LRA: lra}, nil
}

// integratedLUFS implements the BS.1770-4 integrated loudness algorithm on
// pre-decoded stereo PCM samples.
func integratedLUFS(left, right []float64, sampleRate float64) float64 {
	if len(left) == 0 {
		return math.Inf(-1)
	}

	// Step 1: K-weight both channels.
	wL := kWeightChannel(left, sampleRate)
	wR := kWeightChannel(right, sampleRate)

	// Step 2: Slice into 400 ms blocks with 100 ms hops and collect the
	// per-block power (sum of channel mean-squares, as BS.1770 specifies).
	blockSize := int(math.Round(blockSizeSec * sampleRate))
	hopSize := int(math.Round(hopSizeSec * sampleRate))
	n := len(wL)

	var blockPowers []float64
	for start := 0; start+blockSize <= n; start += hopSize {
		end := start + blockSize
		msL := meanSquare(wL[start:end])
		msR := meanSquare(wR[start:end])
		// BS.1770 channel weights: 1.0 for L and R in stereo.
		// The standard sums (not averages) so a dual-mono signal reads ~3 LU
		// louder than a single channel — matching how humans perceive stereo width.
		blockPowers = append(blockPowers, msL+msR)
	}

	if len(blockPowers) == 0 {
		return math.Inf(-1)
	}

	// Step 3: Absolute gating — drop blocks below –70 LUFS.
	// –70 LUFS absolute silence gate prevents noise floors from dragging down
	// the mean. The constant –0.691 in the formula comes from the RMS-to-power
	// conversion in the original BS.1770 derivation.
	var aboveAbs []float64
	for _, p := range blockPowers {
		if blockLUFS(p) >= absGateLevel {
			aboveAbs = append(aboveAbs, p)
		}
	}
	if len(aboveAbs) == 0 {
		return math.Inf(-1)
	}

	// Step 4: Relative gating — drop blocks more than 10 LU below the
	// ungated mean of what survived the absolute gate.
	ungatedMean := mean(aboveAbs)
	relThreshold := blockLUFS(ungatedMean) - 10.0

	var gated []float64
	for _, p := range aboveAbs {
		if blockLUFS(p) >= relThreshold {
			gated = append(gated, p)
		}
	}
	if len(gated) == 0 {
		return math.Inf(-1)
	}

	// Step 5: Final loudness from the gated mean power.
	return blockLUFS(mean(gated))
}

// blockLUFS converts a block's summed mean-square power to LUFS.
// The –0.691 offset is part of the ITU-R BS.1770 definition.
func blockLUFS(sumMeanSquare float64) float64 {
	if sumMeanSquare <= 0 {
		return math.Inf(-1)
	}
	return -0.691 + 10*math.Log10(sumMeanSquare)
}

func meanSquare(s []float64) float64 {
	var sum float64
	for _, v := range s {
		sum += v * v
	}
	return sum / float64(len(s))
}

func mean(s []float64) float64 {
	var sum float64
	for _, v := range s {
		sum += v
	}
	return sum / float64(len(s))
}

// tpFIR holds the order-48, 4-phase FIR interpolating filter from
// ITU-R BS.1770-5 Annex 2 (the 4× over-sampling polyphase filter for
// true-peak estimation). Each phase has 12 taps; phase p produces the
// over-sampled output sample that sits at fraction p/4 between input samples.
//
// Indexing convention: outputs are sum over k of tpFIR[phase][k] · x[n-k],
// so tap 0 multiplies the most recent input sample.
var tpFIR = [4][12]float64{
	{ // phase 0
		0.0017089843750, 0.0109863281250, -0.0196533203125, 0.0332031250000,
		-0.0594482421875, 0.1373291015625, 0.9721679687500, -0.1022949218750,
		0.0476074218750, -0.0266113281250, 0.0148925781250, -0.0083007812500,
	},
	{ // phase 1
		-0.0291748046875, 0.0292968750000, -0.0517578125000, 0.0891113281250,
		-0.1665039062500, 0.4650878906250, 0.7797851562500, -0.2003173828125,
		0.1015625000000, -0.0582275390625, 0.0330810546875, -0.0189208984375,
	},
	{ // phase 2
		-0.0189208984375, 0.0330810546875, -0.0582275390625, 0.1015625000000,
		-0.2003173828125, 0.7797851562500, 0.4650878906250, -0.1665039062500,
		0.0891113281250, -0.0517578125000, 0.0292968750000, -0.0291748046875,
	},
	{ // phase 3
		-0.0083007812500, 0.0148925781250, -0.0266113281250, 0.0476074218750,
		-0.1022949218750, 0.9721679687500, 0.1373291015625, -0.0594482421875,
		0.0332031250000, -0.0196533203125, 0.0109863281250, 0.0017089843750,
	},
}

// truePeak returns the inter-sample true-peak level in dBTP, taking the
// highest across the left and right channels (ITU-R BS.1770-5 Annex 2).
// Returns math.Inf(-1) for a fully silent signal.
func truePeak(left, right []float64) float64 {
	pk := math.Max(channelTruePeakLinear(left), channelTruePeakLinear(right))
	if pk <= 0 {
		return math.Inf(-1)
	}
	// The 12.04 dB attenuate/restore pair in the standard cancels out in
	// floating point, so the linear peak converts straight to dBTP.
	return 20 * math.Log10(pk)
}

// channelTruePeakLinear 4× over-samples one channel through the BS.1770-5
// polyphase FIR and returns the maximum absolute interpolated value (linear).
func channelTruePeakLinear(samples []float64) float64 {
	n := len(samples)
	if n == 0 {
		return 0
	}
	const taps = 12
	var maxAbs float64
	for i := range samples {
		for phase := range 4 {
			var acc float64
			for k := range taps {
				idx := i - k
				if idx < 0 {
					break // samples before the start are zero
				}
				acc += tpFIR[phase][k] * samples[idx]
			}
			if a := math.Abs(acc); a > maxAbs {
				maxAbs = a
			}
		}
	}
	return maxAbs
}

// loudnessRange computes the Loudness Range (LRA) in LU per EBU Tech 3342:
// 3 s short-term blocks at 75% overlap, K-weighted, absolute-gated at
// −70 LUFS, relative-gated at −20 LU below the ungated mean, then
// LRA = 95th percentile − 10th percentile of the surviving block loudnesses.
func loudnessRange(left, right []float64, sampleRate float64) float64 {
	if len(left) == 0 {
		return 0
	}

	wL := kWeightChannel(left, sampleRate)
	wR := kWeightChannel(right, sampleRate)

	blockSize := int(math.Round(lraBlockSizeSec * sampleRate))
	hopSize := int(math.Round(lraHopSizeSec * sampleRate))
	n := len(wL)
	if blockSize <= 0 || hopSize <= 0 || n < blockSize {
		return 0
	}

	var loudness []float64 // per-block LUFS that pass the absolute gate
	var powers []float64   // matching summed mean-square powers
	for start := 0; start+blockSize <= n; start += hopSize {
		end := start + blockSize
		p := meanSquare(wL[start:end]) + meanSquare(wR[start:end])
		l := blockLUFS(p)
		if l >= absGateLevel {
			loudness = append(loudness, l)
			powers = append(powers, p)
		}
	}
	if len(loudness) == 0 {
		return 0
	}

	// Relative gate: −20 LU below the ungated mean (mean of powers, not of LUFS).
	relThreshold := blockLUFS(mean(powers)) + lraRelGate

	var gated []float64
	for i, l := range loudness {
		if l >= relThreshold {
			gated = append(gated, loudness[i])
		}
	}
	if len(gated) < 2 {
		return 0
	}

	sort.Float64s(gated)
	low := percentile(gated, 10)
	high := percentile(gated, 95)
	return high - low
}

// percentile returns the p-th percentile (0..100) of a sorted slice using
// linear interpolation between ranks, as EBU Tech 3342 specifies for LRA.
func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return math.Inf(-1)
	}
	if n == 1 {
		return sorted[0]
	}
	rank := (p / 100) * float64(n-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return sorted[lo]
	}
	frac := rank - float64(lo)
	return sorted[lo] + frac*(sorted[hi]-sorted[lo])
}
