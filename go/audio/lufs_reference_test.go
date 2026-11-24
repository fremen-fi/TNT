package audio

import (
	"math"
	"testing"
)

// Reference-value tests for the loudness meter.
//
// Every signal here is generated in memory and every expected value follows from
// the standards or from simple arithmetic on the signal, so the tests need no audio
// files. The scenarios mirror the EBU Tech 3341 / 3342 compliance material:
// level-linearity, gating and loudness-range steps.

// dbToAmp converts a dBFS level to linear amplitude.
func dbToAmp(db float64) float64 { return math.Pow(10, db/20) }

// stereoSine returns identical L/R sines of the given dBFS level.
func stereoSine(db, freq, sampleRate, seconds float64) (l, r []float64) {
	l = genSine(dbToAmp(db), freq, sampleRate, int(seconds*sampleRate))
	return l, l
}

// concat joins mono sections end to end.
func concat(parts ...[]float64) []float64 {
	var out []float64
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// A 1 kHz stereo sine at −23 dBFS per channel measures −23 LUFS: the K-weighting
// gain at 1 kHz (+0.691 dB) cancels the −0.691 offset in the BS.1770 definition.
// This is EBU Tech 3341 case 1.
func TestReference_integratedSine(t *testing.T) {
	for _, rate := range []float64{44100, 48000, 96000} {
		l, r := stereoSine(-23, 1000, rate, 20)
		got := integratedLUFS(l, r, rate)
		if math.Abs(got-(-23.0)) > 0.1 {
			t.Errorf("%.0f Hz: -23 dBFS sine = %.3f LUFS, want -23.0 ±0.1", rate, got)
		}
	}
}

// Loudness is linear in dB: +10 dB in level gives exactly +10 LU.
func TestReference_levelLinearity(t *testing.T) {
	const rate = 48000.0
	l1, r1 := stereoSine(-33, 1000, rate, 10)
	l2, r2 := stereoSine(-23, 1000, rate, 10)
	diff := integratedLUFS(l2, r2, rate) - integratedLUFS(l1, r1, rate)
	if math.Abs(diff-10) > 1e-6 {
		t.Errorf("+10 dB gave %.6f LU, want 10", diff)
	}
}

// Channel summing per BS.1770: the same sine in only one channel is 3.01 LU
// quieter than in both.
func TestReference_singleChannel(t *testing.T) {
	const rate = 48000.0
	l, r := stereoSine(-23, 1000, rate, 10)
	silent := make([]float64, len(l))
	both := integratedLUFS(l, r, rate)
	one := integratedLUFS(l, silent, rate)
	if math.Abs((both-one)-10*math.Log10(2)) > 1e-6 {
		t.Errorf("both−one = %.6f LU, want 3.0103", both-one)
	}
}

// Absolute gate (−70 LUFS): a −80 dBFS tail must not drag the result down.
// Loud section is long so the few blocks straddling the join stay negligible.
func TestReference_absoluteGate(t *testing.T) {
	const rate = 48000.0
	loud := genSine(dbToAmp(-23), 1000, rate, 60*48000)
	quiet := genSine(dbToAmp(-80), 1000, rate, 20*48000)
	ref := integratedLUFS(loud, loud, rate)
	sig := concat(loud, quiet)
	got := integratedLUFS(sig, sig, rate)
	if math.Abs(got-ref) > 0.05 {
		t.Errorf("with -80 dBFS tail: %.3f LUFS, loud only: %.3f (gate should ignore tail)", got, ref)
	}
}

// Relative gate (−10 LU): a section 20 LU below the loud one must be ignored.
// Mirrors the intent of EBU Tech 3341 case 5.
func TestReference_relativeGate(t *testing.T) {
	const rate = 48000.0
	loud := genSine(dbToAmp(-23), 1000, rate, 60*48000)
	quiet := genSine(dbToAmp(-43), 1000, rate, 20*48000)
	ref := integratedLUFS(loud, loud, rate)
	sig := concat(loud, quiet)
	got := integratedLUFS(sig, sig, rate)
	if math.Abs(got-ref) > 0.1 {
		t.Errorf("with -43 dBFS tail: %.3f LUFS, loud only: %.3f (relative gate should ignore tail)", got, ref)
	}
}

// Loudness range of two equal-length sections is the step between them, as long
// as both pass the −20 LU relative gate. This is how the EBU Tech 3342 tone-step
// cases are built. Steps are asserted to ±0.01 LU, far tighter than the ±1 LU
// the standard allows, so a wrong hop, window or percentile shows up.
func TestReference_LRAStep(t *testing.T) {
	steps := []float64{3, 5, 10, 15}
	for _, rate := range []float64{44100, 48000, 96000} {
		for _, step := range steps {
			high := genSine(dbToAmp(-23), 1000, rate, int(30*rate))
			low := genSine(dbToAmp(-23-step), 1000, rate, int(30*rate))
			sig := concat(high, low)
			got := loudnessRange(sig, sig, rate)
			if math.Abs(got-step) > 0.01 {
				t.Errorf("%.0f Hz, %.0f dB step: LRA = %.4f LU, want %.0f", rate, step, got, step)
			}
		}
	}
}

// Order must not matter: quiet→loud gives the same LRA as loud→quiet.
func TestReference_LRAOrderIndependent(t *testing.T) {
	const rate = 48000.0
	high := genSine(dbToAmp(-23), 1000, rate, 30*48000)
	low := genSine(dbToAmp(-33), 1000, rate, 30*48000)
	a := concat(high, low)
	b := concat(low, high)
	la, lb := loudnessRange(a, a, rate), loudnessRange(b, b, rate)
	if math.Abs(la-lb) > 0.01 {
		t.Errorf("LRA depends on order: %.4f vs %.4f", la, lb)
	}
}

// The −20 LU relative gate: a section 30 LU below must be gated out of LRA, so
// the range is that of the remaining sections (here 5 LU), not 30.
func TestReference_LRARelativeGate(t *testing.T) {
	const rate = 48000.0
	a := genSine(dbToAmp(-23), 1000, rate, 30*48000)
	b := genSine(dbToAmp(-28), 1000, rate, 30*48000)
	c := genSine(dbToAmp(-53), 1000, rate, 5*48000) // short, 30 LU down
	sig := concat(a, b, c)
	got := loudnessRange(sig, sig, rate)
	if math.Abs(got-5) > 0.05 {
		t.Errorf("LRA = %.3f LU, want 5 (the −53 dBFS section is below the relative gate)", got)
	}
}

// Short-term sampling must be dense enough to see a 3 s event wherever it falls
// relative to the hop grid. Shifting the whole signal by sub-hop offsets must
// not move the result by more than a small fraction of a LU.
func TestReference_LRAShiftStable(t *testing.T) {
	const rate = 48000.0
	high := genSine(dbToAmp(-20), 1000, rate, 12*48000)
	low := genSine(dbToAmp(-30), 1000, rate, 40*48000)
	base := concat(low, high, low)
	ref := loudnessRange(base, base, rate)
	for _, shiftMs := range []int{17, 33, 50, 71, 99} {
		pad := make([]float64, shiftMs*48)
		sig := concat(pad, base)
		sig = sig[:len(base)] // same length, content shifted
		got := loudnessRange(sig, sig, rate)
		if math.Abs(got-ref) > 0.3 {
			t.Errorf("shift %d ms: LRA %.3f vs %.3f", shiftMs, got, ref)
		}
	}
}

// True peak of a sine at fs/4 with a 45° phase: the samples only ever reach
// 0.707·A, but the underlying waveform reaches A. The 4× oversampler must
// recover it. This is the idea behind the EBU true-peak test signals.
func TestReference_truePeakIntersample(t *testing.T) {
	const rate = 48000.0
	amp := dbToAmp(-6)
	n := 48000
	ch := make([]float64, n)
	for i := range ch {
		ch[i] = amp * math.Sin(2*math.Pi*(rate/4)*float64(i)/rate+math.Pi/4)
	}
	tp := truePeak(ch, ch)
	// EBU Tech 3341 allows +0.2/−0.4 dB on true-peak readings; the 12-tap
	// interpolator reads about 0.1 dB high here.
	if d := tp - (-6.0206); d > 0.2 || d < -0.4 {
		t.Errorf("true peak = %.3f dBTP, want -6.02 (+0.2/−0.4)", tp)
	}
	var samplePeak float64
	for _, v := range ch {
		samplePeak = math.Max(samplePeak, math.Abs(v))
	}
	if sp := 20 * math.Log10(samplePeak); tp < sp+2.5 {
		t.Errorf("true peak %.3f is not above sample peak %.3f by ~3 dB", tp, sp)
	}
}
