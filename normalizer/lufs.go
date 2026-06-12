package normalizer

// Streaming EBU R128 loudness measurement (ITU-R BS.1770-4).
//
// Memory model: block energies are stored as one float64 per 100 ms step.
// A 3-hour stereo file at 44100 Hz produces at most 108 000 blocks ≈ 864 KiB.
// No sample arrays are allocated beyond the processing buffer passed in.

import (
	"fmt"
	"io"
	"math"
	"sort"
)

// LoudnessResult carries the measurements returned by MeasureLUFS.
type LoudnessResult struct {
	IntegratedLUFS float64 // EBU R128 integrated program loudness (LUFS)
	LRA            float64 // Loudness range (LU)
	SamplePeakDB   float64 // Highest sample peak across all channels (dBFS)
}

// MeasureLUFS performs a single streaming pass over interleaved f32 stereo PCM
// read from r, computing EBU R128 integrated loudness, LRA, and sample peak.
//
// sampleRate must be the PCM stream's sample rate (e.g. 44100).
// r must be positioned at the first PCM sample byte (after the WAV data header).
// dataBytes is the number of PCM bytes to read (-1 = read until EOF).
func MeasureLUFS(r io.Reader, sampleRate int, dataBytes int64) (LoudnessResult, error) {
	if sampleRate <= 0 {
		return LoudnessResult{}, fmt.Errorf("invalid sample rate %d", sampleRate)
	}

	filters := newKweightFilters(float64(sampleRate))

	// Block sizes in samples (per channel).
	step := sampleRate / 10         // 100 ms step
	blockLen := 4 * sampleRate / 10 // 400 ms block = 4 steps

	// Short-term block sizes for LRA.
	stStep := sampleRate         // 1 s step
	stBlockLen := 3 * sampleRate // 3 s block

	// We accumulate a ring buffer of the last blockLen per-step mean-squares
	// so we can form the 400 ms sliding window without re-reading samples.
	stepEnergies := make([]float64, 0, 4096) // mean-square per 100 ms step
	stStepEnergies := make([]float64, 0, 4096)

	// Per-step accumulators (reset every `step` samples).
	var stepSumSq float64
	var stepSamples int

	// Per-short-term-step (1 s) accumulators.
	var stSumSq float64
	var stSamples int

	var samplePeak float64 // max |sample| (linear)

	// Read buffer: process in chunks of `step` interleaved samples (2 channels).
	chunkSize := step * 2 // interleaved L+R
	buf := make([]float32, chunkSize)

	var totalRead int64

	for {
		if dataBytes >= 0 && totalRead >= dataBytes {
			break
		}
		toRead := chunkSize
		if dataBytes >= 0 {
			bytesLeft := dataBytes - totalRead
			samplesLeft := int(bytesLeft / 4)
			if samplesLeft < toRead {
				toRead = samplesLeft
			}
		}
		if toRead == 0 {
			break
		}

		n, err := readF32LE(r, buf[:toRead])
		totalRead += int64(n * 4)

		for i := 0; i < n; i += 2 {
			l := float64(buf[i])
			r := float64(buf[i+1])

			// Track sample peak before K-weighting.
			if a := math.Abs(l); a > samplePeak {
				samplePeak = a
			}
			if a := math.Abs(r); a > samplePeak {
				samplePeak = a
			}

			// Apply K-weighting per channel.
			kl := filters[0].process(l)
			kr := filters[1].process(r)

			// Mean square contribution (average over channels).
			ms := (kl*kl + kr*kr) * 0.5
			stepSumSq += ms
			stepSamples++

			stSumSq += ms
			stSamples++
		}

		// Emit a 100 ms block when full.
		if stepSamples >= step {
			stepEnergies = append(stepEnergies, stepSumSq/float64(stepSamples))
			stepSumSq = 0
			stepSamples = 0
		}

		// Emit a 1 s short-term block.
		if stSamples >= stStep {
			stStepEnergies = append(stStepEnergies, stSumSq/float64(stSamples))
			stSumSq = 0
			stSamples = 0
		}

		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return LoudnessResult{}, err
		}
	}

	// Flush partial step.
	if stepSamples > 0 {
		stepEnergies = append(stepEnergies, stepSumSq/float64(stepSamples))
	}
	if stSamples > 0 {
		stStepEnergies = append(stStepEnergies, stSumSq/float64(stSamples))
	}

	intLUFS := integratedLoudness(stepEnergies, blockLen/step)
	lra := loudnessRange(stStepEnergies, stBlockLen/stStep)

	var peakDB float64
	if samplePeak > 0 {
		peakDB = 20 * math.Log10(samplePeak)
	} else {
		peakDB = -math.MaxFloat64
	}

	return LoudnessResult{
		IntegratedLUFS: intLUFS,
		LRA:            lra,
		SamplePeakDB:   peakDB,
	}, nil
}

// integratedLoudness computes EBU R128 integrated loudness from per-step mean
// squares. stepsPerBlock is the number of steps that make up one gating block
// (4 for 100 ms steps / 400 ms blocks).
func integratedLoudness(stepMSq []float64, stepsPerBlock int) float64 {
	if len(stepMSq) < stepsPerBlock {
		return math.Inf(-1)
	}

	// Build 400 ms sliding-window block powers.
	blocks := make([]float64, 0, len(stepMSq))
	for i := 0; i+stepsPerBlock <= len(stepMSq); i++ {
		var sum float64
		for j := range stepsPerBlock {
			sum += stepMSq[i+j]
		}
		blocks = append(blocks, sum/float64(stepsPerBlock))
	}

	// Absolute gate: -70 LUFS → linear threshold.
	absThresh := math.Pow(10, (-70.0+0.691)/10) // 0.691 is the offset for mean-square

	// First pass: mean of blocks above absolute gate.
	var sumAbove float64
	var countAbove int
	for _, b := range blocks {
		if b >= absThresh {
			sumAbove += b
			countAbove++
		}
	}
	if countAbove == 0 {
		return math.Inf(-1)
	}
	ungatedMean := sumAbove / float64(countAbove)

	// Relative gate: ungated mean − 10 LU.
	relThresh := ungatedMean * math.Pow(10, -10.0/10)

	// Second pass: mean of blocks above relative gate.
	var sumRel float64
	var countRel int
	for _, b := range blocks {
		if b >= relThresh {
			sumRel += b
			countRel++
		}
	}
	if countRel == 0 {
		return math.Inf(-1)
	}

	integratedMean := sumRel / float64(countRel)
	return -0.691 + 10*math.Log10(integratedMean)
}

// loudnessRange computes LRA from per-1s-step mean squares.
// stepsPerSTBlock = 3 (3 s blocks with 1 s steps).
func loudnessRange(stStepMSq []float64, stepsPerSTBlock int) float64 {
	if len(stStepMSq) < stepsPerSTBlock {
		return 0
	}

	stBlocks := make([]float64, 0, len(stStepMSq))
	for i := 0; i+stepsPerSTBlock <= len(stStepMSq); i++ {
		var sum float64
		for j := range stepsPerSTBlock {
			sum += stStepMSq[i+j]
		}
		stBlocks = append(stBlocks, sum/float64(stepsPerSTBlock))
	}

	// Absolute gate: -70 LUFS.
	absThresh := math.Pow(10, (-70.0+0.691)/10)
	var above []float64
	for _, b := range stBlocks {
		if b >= absThresh {
			above = append(above, b)
		}
	}
	if len(above) == 0 {
		return 0
	}

	// Relative gate: mean of above-absolute minus 20 LU.
	var sumA float64
	for _, b := range above {
		sumA += b
	}
	relThresh := (sumA / float64(len(above))) * math.Pow(10, -20.0/10)

	var gated []float64
	for _, b := range above {
		if b >= relThresh {
			gated = append(gated, b)
		}
	}
	if len(gated) < 2 {
		return 0
	}

	// Sort and take 10th – 95th percentile range.
	sort.Float64s(gated)
	lo := gated[int(math.Round(float64(len(gated)-1)*0.10))]
	hi := gated[int(math.Round(float64(len(gated)-1)*0.95))]

	var luLo, luHi float64
	if lo > 0 {
		luLo = -0.691 + 10*math.Log10(lo)
	}
	if hi > 0 {
		luHi = -0.691 + 10*math.Log10(hi)
	}
	lra := luHi - luLo
	if lra < 0 {
		return 0
	}
	return lra
}

// kweightFilter is a single biquad IIR section (Direct Form I).
type kweightFilter struct {
	b0, b1, b2 float64
	a1, a2     float64
	x1, x2     float64 // input delay
	y1, y2     float64 // output delay
}

func (f *kweightFilter) process(x float64) float64 {
	y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
	f.x2, f.x1 = f.x1, x
	f.y2, f.y1 = f.y1, y
	return y
}

// channelFilter chains stage1 (high-shelf pre-filter) and stage2 (high-pass).
type channelFilter struct {
	s1, s2 kweightFilter
}

func (cf *channelFilter) process(x float64) float64 {
	return cf.s2.process(cf.s1.process(x))
}

// newKweightFilters returns K-weighting biquad pairs for stereo (L, R).
// Coefficients are derived analytically from ITU-R BS.1770-4 / libebur128.
func newKweightFilters(fs float64) [2]channelFilter {
	var cf [2]channelFilter
	for i := range cf {
		cf[i].s1 = kweightStage1(fs)
		cf[i].s2 = kweightStage2(fs)
	}
	return cf
}

func kweightStage1(fs float64) kweightFilter {
	// High-shelf pre-filter: f0=1681.97 Hz, G=+4 dB, Q=0.707
	f0 := 1681.974450955533
	G := 3.999843853973347
	Q := 0.7071752369554196

	K := math.Tan(math.Pi * f0 / fs)
	Vh := math.Pow(10.0, G/20.0)
	Vb := math.Pow(Vh, 0.4845)

	a0 := 1.0 + K/Q + K*K
	return kweightFilter{
		b0: (Vh + Vb*K/Q + K*K) / a0,
		b1: 2.0 * (K*K - Vh) / a0,
		b2: (Vh - Vb*K/Q + K*K) / a0,
		a1: 2.0 * (K*K - 1.0) / a0,
		a2: (1.0 - K/Q + K*K) / a0,
	}
}

func kweightStage2(fs float64) kweightFilter {
	// High-pass (RLB) filter: f0=38.14 Hz, Q=0.500
	f0 := 38.13547087692325
	Q := 0.5003270373238773

	K := math.Tan(math.Pi * f0 / fs)
	a0 := 1.0 + K/Q + K*K
	return kweightFilter{
		b0: 1.0 / a0,
		b1: -2.0 / a0,
		b2: 1.0 / a0,
		a1: 2.0 * (K*K - 1.0) / a0,
		a2: (1.0 - K/Q + K*K) / a0,
	}
}
