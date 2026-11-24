package normalizer

import (
	"math"
	"math/rand"
	"testing"
)

// ── long reference true-peak interpolator ───────────────────────────────────
//
// 8x windowed-sinc (Blackman), 128 taps per output point, independent of tpFIR.
// y(n+f) = Σ_j x[n+j]·sinc(j−f)·w(j−f), j ∈ [−refHalf+1, refHalf], f = p/8.

const (
	refOversample = 8
	refHalf       = 64 // 128 taps
)

var refKernel = func() (k [refOversample][2 * refHalf]float64) {
	for p := 0; p < refOversample; p++ {
		f := float64(p) / refOversample
		for t := 0; t < 2*refHalf; t++ {
			j := float64(t-refHalf+1) - f // distance from the interpolation point
			var s float64
			if j == 0 {
				s = 1
			} else {
				s = math.Sin(math.Pi*j) / (math.Pi * j)
			}
			w := 0.42 + 0.5*math.Cos(math.Pi*j/refHalf) + 0.08*math.Cos(2*math.Pi*j/refHalf)
			if math.Abs(j) >= refHalf {
				w = 0
			}
			k[p][t] = s * w
		}
	}
	return k
}()

// refTruePeak returns the maximum |value| of the 8x-reconstructed waveform of x,
// treating x as zero outside its bounds.
func refTruePeak(x []float64) float64 {
	var mx float64
	n := len(x)
	for i := -refHalf; i < n+refHalf; i++ {
		for p := 0; p < refOversample; p++ {
			var acc float64
			for t := 0; t < 2*refHalf; t++ {
				idx := i + t - refHalf + 1
				if idx < 0 || idx >= n {
					continue
				}
				acc += x[idx] * refKernel[p][t]
			}
			if a := math.Abs(acc); a > mx {
				mx = a
			}
		}
	}
	return mx
}

func refTruePeakStereo(l, r []float64) float64 {
	return math.Max(refTruePeak(l), refTruePeak(r))
}

// detectorTruePeak is the repo's 12-tap detector peak over a stereo signal.
func detectorTruePeak(l, r []float64) float64 {
	var hL, hR tpHist
	var mx float64
	for i := range l {
		hL.push(l[i])
		hR.push(r[i])
		if v := interpMax(&hL, &hR); v > mx {
			mx = v
		}
	}
	return mx
}

func dB(x float64) float64 { return 20 * math.Log10(x) }

// ── signal generators ───────────────────────────────────────────────────────

func whiteNoise(n int, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	x := make([]float64, n)
	for i := range x {
		x[i] = 2*rng.Float64() - 1
	}
	return x
}

// bandLimitedNoise is white noise low-passed below ~0.45 fs (windowed-sinc FIR),
// scaled so its SAMPLE peak is exactly 1.0 (full scale).
func bandLimitedNoise(n int, seed int64) []float64 {
	const taps = 127
	const fc = 0.45
	h := make([]float64, taps)
	for i := range h {
		j := float64(i - taps/2)
		var s float64
		if j == 0 {
			s = 2 * fc
		} else {
			s = math.Sin(2*math.Pi*fc*j) / (math.Pi * j)
		}
		h[i] = s * (0.42 - 0.5*math.Cos(2*math.Pi*float64(i)/(taps-1)) + 0.08*math.Cos(4*math.Pi*float64(i)/(taps-1)))
	}
	w := whiteNoise(n+taps, seed)
	x := make([]float64, n)
	var pk float64
	for i := range x {
		var acc float64
		for k, c := range h {
			acc += c * w[i+k]
		}
		x[i] = acc
		if a := math.Abs(acc); a > pk {
			pk = a
		}
	}
	for i := range x {
		x[i] /= pk
	}
	return x
}

// sineAt returns a sine with a Hann fade in/out (256 samples) so the gated
// edges do not splatter and fake a Gibbs overshoot in the reference; the
// interior is exactly amp.
func sineAt(n int, cyclesPerSample, phase, amp float64) []float64 {
	const ramp = 256
	x := make([]float64, n)
	for i := range x {
		env := 1.0
		if i < ramp {
			env = 0.5 - 0.5*math.Cos(math.Pi*float64(i)/ramp)
		} else if i >= n-ramp {
			env = 0.5 - 0.5*math.Cos(math.Pi*float64(n-1-i)/ramp)
		}
		x[i] = env * amp * math.Sin(2*math.Pi*cyclesPerSample*float64(i)+phase)
	}
	return x
}

// worstSineNearQuarter sweeps frequencies around fs/4 and phase offsets (full
// scale sine) and returns the worst detector under-read (dB) found.
func worstSineNearQuarter() (worstDB, atFreq, atPhase float64) {
	const n = 2048
	worstDB = math.Inf(-1)
	for fi := 0; fi <= 20; fi++ {
		freq := 0.20 + 0.10*float64(fi)/20 // 0.20..0.30 fs
		for pi := 0; pi < 16; pi++ {
			ph := 2 * math.Pi * float64(pi) / 16
			x := sineAt(n, freq, ph, 1)
			ref := refTruePeak(x)
			det := detectorTruePeak(x, x)
			if e := dB(ref / det); e > worstDB {
				worstDB, atFreq, atPhase = e, freq, ph
			}
		}
	}
	return
}

// TestTruePeakDetectorError measures how far the 12-tap tpFIR detector under-reads
// the true peak versus a 128-tap 8x reference. Informational: it logs the numbers
// (for the tap-count decision) and only fails on gross errors.
func TestTruePeakDetectorError(t *testing.T) {
	worst, f, ph := worstSineNearQuarter()
	t.Logf("sine near fs/4 (0.20-0.30 fs x 16 phases): worst detector under-read %.3f dB (at %.3f fs, phase %.2f rad)", worst, f, ph)

	const n = 1 << 15
	worstBL, worstWN := math.Inf(-1), math.Inf(-1)
	for seed := int64(1); seed <= 8; seed++ {
		bl := bandLimitedNoise(n, seed)
		if e := dB(refTruePeak(bl) / detectorTruePeak(bl, bl)); e > worstBL {
			worstBL = e
		}
		wn := whiteNoise(n, 100+seed)
		if e := dB(refTruePeak(wn) / detectorTruePeak(wn, wn)); e > worstWN {
			worstWN = e
		}
	}
	t.Logf("full-scale band-limited noise (8 seeds): worst detector under-read %.3f dB", worstBL)
	t.Logf("white noise (8 seeds): worst detector under-read %.3f dB", worstWN)

	for name, e := range map[string]float64{"sine": worst, "bandlimited": worstBL, "white": worstWN} {
		if e > 3.0 {
			t.Errorf("%s: detector under-read %.3f dB is implausibly large (reference or detector broken?)", name, e)
		}
	}
}
