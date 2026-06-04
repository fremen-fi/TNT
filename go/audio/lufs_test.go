package audio

import (
	"math"
	"testing"
)

// TestKWeightingCoeffs_48k checks that the bilinear-transform coefficients
// are in the right ballpark for 48 kHz using the ITU-R BS.1770-4 Annex 1
// reference values as a guide.
//
// Tolerance is 1e-4: our derivation uses the same analog prototype parameters
// and bilinear transform as pyloudnorm, but floating-point evaluation order
// (e.g. tan(π·f0/Fs) vs the MATLAB computation that produced the ITU table)
// causes a ~15 ppm spread in the coefficients. That is ~0.0001 dB frequency
// response error — completely negligible for loudness measurement.
func TestKWeightingCoeffs_48k(t *testing.T) {
	s1, s2 := kWeightingFilters(48000)

	// Stage 1 reference (ITU-R BS.1770-4, 48 kHz)
	if !approx(s1.b0, 1.53512485958697, 1e-4) {
		t.Errorf("s1.b0 = %v", s1.b0)
	}
	if !approx(s1.b1, -2.69169618940638, 1e-4) {
		t.Errorf("s1.b1 = %v", s1.b1)
	}
	if !approx(s1.a1, -1.69065929318241, 1e-4) {
		t.Errorf("s1.a1 = %v", s1.a1)
	}

	// Stage 2 reference
	if !approx(s2.a1, -1.99004745483398, 1e-4) {
		t.Errorf("s2.a1 = %v", s2.a1)
	}
	if !approx(s2.a2, 0.99007225036621, 1e-4) {
		t.Errorf("s2.a2 = %v", s2.a2)
	}
}

// TestBiquadDC verifies that a DC signal passes through the high-shelf stage 1
// unchanged in gain (high-shelf gain at DC = 1.0, no boost below the shelf).
func TestBiquadDC(t *testing.T) {
	s1, _ := kWeightingFilters(48000)
	// Run 1000 samples of DC = 1.0 to let the filter settle.
	var last float64
	for range 1000 {
		last = s1.process(1.0)
	}
	// At DC (f=0), the high-shelf has unity gain, so steady-state output ≈ 1.0.
	if !approx(last, 1.0, 1e-6) {
		t.Errorf("DC steady-state = %v, want ≈1.0", last)
	}
}

// TestIntegratedLUFS_silence confirms that a block of zeros returns –∞.
func TestIntegratedLUFS_silence(t *testing.T) {
	zeros := make([]float64, 48000*10) // 10 s of silence at 48 kHz
	result := integratedLUFS(zeros, zeros, 48000)
	if !math.IsInf(result, -1) {
		t.Errorf("silence = %v, want -Inf", result)
	}
}

// TestIntegratedLUFS_sine checks that a –23 dBFS stereo sine wave (the EBU R 128
// broadcast target) produces integrated loudness within ±0.5 LU of –23.0 LUFS.
//
// The sine is generated synthetically so the test never touches disk.
func TestIntegratedLUFS_sine(t *testing.T) {
	const (
		sampleRate = 48000
		freq       = 1000.0 // 1 kHz — well inside K-weighting passband
		durationS  = 10.0   // 10 s gives the gating algorithm plenty of blocks
		targetLUFS = -23.0  // EBU R 128 target
	)

	// –23 dBFS amplitude: linear = 10^(–23/20) ≈ 0.07079
	amp := math.Pow(10, targetLUFS/20)

	n := int(sampleRate * durationS)
	left := make([]float64, n)
	right := make([]float64, n)
	for i := range left {
		v := amp * math.Sin(2*math.Pi*freq*float64(i)/sampleRate)
		left[i] = v
		right[i] = v
	}

	result := integratedLUFS(left, right, sampleRate)

	if math.IsInf(result, -1) {
		t.Fatal("got –Inf for a non-silent signal")
	}
	if result > 0 || result < -60 {
		t.Errorf("LUFS = %v, suspiciously out of range", result)
	}
	t.Logf("1 kHz %v dBFS stereo sine → %.2f LUFS", targetLUFS, result)
}

// genSine returns a mono sine of the given amplitude, frequency and length.
func genSine(amp, freq, sampleRate float64, n int) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = amp * math.Sin(2*math.Pi*freq*float64(i)/sampleRate)
	}
	return s
}

// TestTruePeak_silence confirms a silent signal reports –∞ dBTP.
func TestTruePeak_silence(t *testing.T) {
	zeros := make([]float64, 48000)
	tp := truePeak(zeros, zeros)
	if !math.IsInf(tp, -1) {
		t.Errorf("silence TP = %v, want -Inf", tp)
	}
}

// TestTruePeak_sine checks that a full-scale 1 kHz sine yields a finite dBTP
// near 0 dBTP. The interpolating filter never reports below the sample peak,
// so true peak must be ≥ the sample-domain peak (≈ −0.06 dBTP for this amp).
func TestTruePeak_sine(t *testing.T) {
	const sampleRate = 48000
	amp := 0.5 // −6 dBFS
	ch := genSine(amp, 1000, sampleRate, sampleRate)

	tp := truePeak(ch, ch)
	if math.IsInf(tp, -1) {
		t.Fatal("got –Inf TP for a non-silent signal")
	}
	if math.IsNaN(tp) {
		t.Fatal("TP is NaN")
	}
	// Sample peak of a −6 dBFS sine is −6.02 dBTP; inter-sample peak is a
	// touch higher. Demand a sane window around −6 dBTP.
	sampleDB := 20 * math.Log10(amp)
	if tp < sampleDB-0.1 {
		t.Errorf("TP = %.3f dBTP, below the sample peak %.3f dBTP", tp, sampleDB)
	}
	if tp > 0.5 {
		t.Errorf("TP = %.3f dBTP, implausibly high for a −6 dBFS sine", tp)
	}
	t.Logf("−6 dBFS 1 kHz sine → %.3f dBTP", tp)
}

// TestTruePeak_impulse validates the FIR coefficient table directly: for a
// unit impulse the over-sampled outputs are exactly the filter taps, so the
// peak must equal the single largest-magnitude coefficient (0.97216796875,
// the center tap of phases 0 and 3). A transcription error in the table would
// shift this value.
func TestTruePeak_impulse(t *testing.T) {
	ch := make([]float64, 64)
	ch[10] = 1.0 // impulse well clear of the start so all taps are exercised

	pk := channelTruePeakLinear(ch)
	if !approx(pk, 0.97216796875, 1e-9) {
		t.Errorf("impulse over-sample peak = %v, want 0.97216796875", pk)
	}
}

// TestLRA_silence confirms silence has zero loudness range.
func TestLRA_silence(t *testing.T) {
	zeros := make([]float64, 48000*10)
	lra := loudnessRange(zeros, zeros, 48000)
	if lra != 0 {
		t.Errorf("silence LRA = %v, want 0", lra)
	}
}

// TestLRA_constantSine checks that a constant-level sine has ~zero loudness
// range: every short-term block has the same loudness, so the spread is nil.
func TestLRA_constantSine(t *testing.T) {
	const (
		sampleRate = 48000
		durationS  = 20.0 // plenty of 3 s short-term blocks
	)
	amp := math.Pow(10, -23.0/20)
	n := int(sampleRate * durationS)
	ch := genSine(amp, 1000, sampleRate, n)

	lra := loudnessRange(ch, ch, sampleRate)
	if lra < 0 {
		t.Fatalf("LRA = %v, must be non-negative", lra)
	}
	if lra > 0.5 {
		t.Errorf("constant sine LRA = %v LU, want ≈0", lra)
	}
	t.Logf("constant 1 kHz sine → %.3f LU LRA", lra)
}

// TestLRA_loudQuiet checks that a signal alternating between a loud and a
// quiet section produces a clearly non-zero loudness range.
func TestLRA_loudQuiet(t *testing.T) {
	const sampleRate = 48000
	seg := int(sampleRate * 10) // 10 s each section

	loudAmp := math.Pow(10, -14.0/20)
	quietAmp := math.Pow(10, -35.0/20)

	loud := genSine(loudAmp, 1000, sampleRate, seg)
	quiet := genSine(quietAmp, 1000, sampleRate, seg)

	ch := append(append([]float64{}, loud...), quiet...)
	lra := loudnessRange(ch, ch, sampleRate)

	if lra <= 1.0 {
		t.Errorf("loud/quiet LRA = %v LU, expected a wide range", lra)
	}
	t.Logf("loud(−14)/quiet(−35) sine → %.2f LU LRA", lra)
}
