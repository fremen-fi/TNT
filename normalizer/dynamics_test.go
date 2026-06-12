package normalizer

import (
	"bytes"
	"math"
	"testing"
)

// ── reference implementations (straightforward whole-buffer ports of the
// go/audio cores) the streaming processors must match sample-for-sample ───────

func refProcessDynamics(left, right []float64, sr, thresholdDb, ratio, kneeDb, attackMs, releaseMs, makeupDb, rmsMs float64) {
	n := min(len(left), len(right))
	attCoef := onePoleCoef(attackMs, sr)
	relCoef := onePoleCoef(releaseMs, sr)
	rmsCoef := onePoleCoef(rmsMs, sr)
	makeup := dbToLin(makeupDb)
	useRMS := rmsMs > 0
	var meanSq, gain float64
	gain = 1.0
	for i := 0; i < n; i++ {
		var level float64
		if useRMS {
			power := 0.5 * (left[i]*left[i] + right[i]*right[i])
			meanSq = rmsCoef*meanSq + (1-rmsCoef)*power
			level = math.Sqrt(meanSq)
		} else {
			level = math.Max(math.Abs(left[i]), math.Abs(right[i]))
		}
		target := dbToLin(staticGainDb(linToDb(level), thresholdDb, ratio, kneeDb))
		if target < gain {
			gain = attCoef*gain + (1-attCoef)*target
		} else {
			gain = relCoef*gain + (1-relCoef)*target
		}
		left[i] *= gain * makeup
		right[i] *= gain * makeup
	}
}

func refLookaheadLimit(left, right []float64, sr, thresholdDb, lookaheadMs, releaseMs float64) {
	n := min(len(left), len(right))
	if n == 0 {
		return
	}
	thresh := dbToLin(thresholdDb)
	look := int(math.Round(lookaheadMs / 1000.0 * sr))
	if look < 1 {
		look = 1
	}
	if look > n-1 {
		look = n - 1
	}
	required := make([]float64, n)
	for i := 0; i < n; i++ {
		var mx float64
		for phase := 0; phase < 4; phase++ {
			var accL, accR float64
			for k := 0; k < 12; k++ {
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
	// forward sliding-window minimum, window [i, i+look]
	winMin := make([]float64, n)
	for i := 0; i < n; i++ {
		m := required[i]
		end := min(i+look, n-1)
		for j := i + 1; j <= end; j++ {
			if required[j] < m {
				m = required[j]
			}
		}
		winMin[i] = m
	}
	relCoef := onePoleCoef(releaseMs, sr)
	gain := 1.0
	for i := 0; i < n; i++ {
		t := winMin[i]
		if t < gain {
			gain = t
		} else {
			gain = relCoef*gain + (1-relCoef)*t
		}
		left[i] = clampMag(left[i]*gain, thresh)
		right[i] = clampMag(right[i]*gain, thresh)
	}
}

// testSignal builds a deterministic stereo signal: mixed sines plus periodic
// transient spikes (so the limiter has real true-peak work to do).
func testSignal(n int) (l, r []float64) {
	l = make([]float64, n)
	r = make([]float64, n)
	for i := 0; i < n; i++ {
		t := float64(i)
		v := 0.4*math.Sin(2*math.Pi*440*t/44100) + 0.3*math.Sin(2*math.Pi*1300*t/44100)
		if i%2000 == 0 {
			v += 0.9 // transient
		}
		l[i] = v
		r[i] = 0.95 * v
	}
	return l, r
}

func maxAbsDiff(a, b []float64) float64 {
	var d float64
	for i := range a {
		if x := math.Abs(a[i] - b[i]); x > d {
			d = x
		}
	}
	return d
}

func TestCompressorMatchesReference(t *testing.T) {
	const sr = 44100
	l, r := testSignal(50000)
	refL, refR := append([]float64(nil), l...), append([]float64(nil), r...)
	refProcessDynamics(refL, refR, sr, -24, 3, 6, 100, 1500, 0, 300)

	c := newCompressor(sr, -24, 3, 6, 100, 1500, 0, 300)
	outL := make([]float64, len(l))
	outR := make([]float64, len(r))
	for i := range l {
		outL[i], outR[i] = c.step(l[i], r[i])
	}
	if d := maxAbsDiff(outL, refL); d > 1e-12 {
		t.Errorf("compressor L diverges from reference by %g", d)
	}
	if d := maxAbsDiff(outR, refR); d > 1e-12 {
		t.Errorf("compressor R diverges from reference by %g", d)
	}
}

func TestCharacterLimiterMatchesReference(t *testing.T) {
	const sr = 44100
	l, r := testSignal(40000)
	refL, refR := append([]float64(nil), l...), append([]float64(nil), r...)
	refProcessDynamics(refL, refR, sr, -10, math.Inf(1), 6, 150, 1200, 0, 0)

	c := newCompressor(sr, -10, math.Inf(1), 6, 150, 1200, 0, 0)
	outL := make([]float64, len(l))
	outR := make([]float64, len(r))
	for i := range l {
		outL[i], outR[i] = c.step(l[i], r[i])
	}
	if d := maxAbsDiff(outL, refL); d > 1e-12 {
		t.Errorf("character limiter L diverges by %g", d)
	}
	if d := maxAbsDiff(outR, refR); d > 1e-12 {
		t.Errorf("character limiter R diverges by %g", d)
	}
}

func refUpwardCompress(left, right []float64, sr, thresholdDb, ratio, kneeDb, attackMs, releaseMs, maxBoostDb, rmsMs float64) {
	n := min(len(left), len(right))
	attCoef := onePoleCoef(attackMs, sr)
	relCoef := onePoleCoef(releaseMs, sr)
	rmsCoef := onePoleCoef(rmsMs, sr)
	useRMS := rmsMs > 0
	var meanSq float64
	gain := 1.0
	for i := 0; i < n; i++ {
		var level float64
		if useRMS {
			power := 0.5 * (left[i]*left[i] + right[i]*right[i])
			meanSq = rmsCoef*meanSq + (1-rmsCoef)*power
			level = math.Sqrt(meanSq)
		} else {
			level = math.Max(math.Abs(left[i]), math.Abs(right[i]))
		}
		target := dbToLin(upwardGainDb(linToDb(level), thresholdDb, ratio, kneeDb, maxBoostDb))
		if target > gain {
			gain = attCoef*gain + (1-attCoef)*target
		} else {
			gain = relCoef*gain + (1-relCoef)*target
		}
		left[i] *= gain
		right[i] *= gain
	}
}

func TestUpwardCompressorMatchesReference(t *testing.T) {
	const sr = 44100
	l, r := testSignal(50000)
	for i := range l { // scale so material sits both sides of the threshold
		l[i] *= 0.3
		r[i] *= 0.3
	}
	refL, refR := append([]float64(nil), l...), append([]float64(nil), r...)
	refUpwardCompress(refL, refR, sr, -24, 3, 12, 300, 600, 5, 300)

	u := newUpwardCompressor(sr, -24, 3, 12, 300, 600, 5, 300)
	outL := make([]float64, len(l))
	outR := make([]float64, len(r))
	for i := range l {
		outL[i], outR[i] = u.step(l[i], r[i])
	}
	if d := maxAbsDiff(outL, refL); d > 1e-12 {
		t.Errorf("upward compressor L diverges by %g", d)
	}
	if d := maxAbsDiff(outR, refR); d > 1e-12 {
		t.Errorf("upward compressor R diverges by %g", d)
	}
}

// convergeChain runs the converging multi-pass over in-memory PCM, mirroring the
// podcore orchestration: measure → derive one stage → apply → re-measure → stop
// at target / diminishing returns / maxPasses, then gain-to-target + conformance
// limit. Returns the final output's measurement, the output PCM, and pass count.
func convergeChain(t *testing.T, sr int, pcm []byte, targetLUFS, targetTP, targetLRA float64) (ChainMeasurement, []byte, int) {
	t.Helper()
	const maxPasses = 6
	m, err := MeasureChain(bytes.NewReader(pcm), sr)
	if err != nil {
		t.Fatal(err)
	}
	cur := pcm
	prevLRA := m.LRA
	passes := 0
	for passes < maxPasses && !math.IsInf(m.IntegratedLUFS, -1) && m.LRA > targetLRA {
		sp := DeriveStage(m, targetLRA)
		var next bytes.Buffer
		if err := ApplyStage(bytes.NewReader(cur), &next, sr, sp); err != nil {
			t.Fatal(err)
		}
		cur = next.Bytes()
		if m, err = MeasureChain(bytes.NewReader(cur), sr); err != nil {
			t.Fatal(err)
		}
		passes++
		if prevLRA-m.LRA < 0.3 { // diminishing returns
			break
		}
		prevLRA = m.LRA
	}
	gainDB := 0.0
	if !math.IsInf(m.IntegratedLUFS, -1) {
		gainDB = targetLUFS - m.IntegratedLUFS
	}
	var out bytes.Buffer
	if err := Conform(bytes.NewReader(cur), &out, sr, gainDB, targetTP, 20, 80); err != nil {
		t.Fatal(err)
	}
	fm, err := MeasureChain(bytes.NewReader(out.Bytes()), sr)
	if err != nil {
		t.Fatal(err)
	}
	return fm, out.Bytes(), passes
}

// TestChainReducesLRA confirms the converging multi-pass meaningfully shrinks the
// loudness range — downward compression alone buys only ~0.5-1 LU.
func TestChainReducesLRA(t *testing.T) {
	const sr = 44100
	const seg = 3 * sr // 3 s segments
	n := seg * 6       // alternating loud / quiet, 18 s
	buf := make([]float32, n*2)
	for i := 0; i < n; i++ {
		amp := 0.3 // loud
		if (i/seg)%2 == 1 {
			amp = 0.05 // quiet
		}
		v := float32(amp * math.Sin(2*math.Pi*300*float64(i)/sr))
		buf[i*2] = v
		buf[i*2+1] = v
	}
	var src bytes.Buffer
	writeF32LE(&src, buf)

	m1, err := MeasureChain(bytes.NewReader(src.Bytes()), sr)
	if err != nil {
		t.Fatal(err)
	}
	final, _, passes := convergeChain(t, sr, src.Bytes(), -18, -5, 7)
	t.Logf("LRA: input=%.1f LU → %.1f LU in %d passes (Δ %.1f)", m1.LRA, final.LRA, passes, m1.LRA-final.LRA)
	if final.LRA >= m1.LRA {
		t.Errorf("chain did not reduce LRA: %.1f → %.1f", m1.LRA, final.LRA)
	}
	if m1.LRA-final.LRA < 3 {
		t.Errorf("LRA reduction too small: %.1f → %.1f (Δ %.1f LU)", m1.LRA, final.LRA, m1.LRA-final.LRA)
	}
}

func TestConformanceLimiterMatchesReference(t *testing.T) {
	const sr = 44100
	l, r := testSignal(60000)
	refL, refR := append([]float64(nil), l...), append([]float64(nil), r...)
	refLookaheadLimit(refL, refR, sr, -5, 20, 80)

	lim := newConformanceLimiter(sr, -5, 20, 80)
	outL := make([]float64, 0, len(l))
	outR := make([]float64, 0, len(r))
	for i := range l {
		if ol, or, ok := lim.push(l[i], r[i]); ok {
			outL = append(outL, ol)
			outR = append(outR, or)
		}
	}
	lim.flush(func(a, b float64) {
		outL = append(outL, a)
		outR = append(outR, b)
	})

	if len(outL) != len(l) {
		t.Fatalf("streaming limiter emitted %d frames, want %d", len(outL), len(l))
	}
	if d := maxAbsDiff(outL, refL); d > 1e-12 {
		t.Errorf("conformance limiter L diverges by %g", d)
	}
	if d := maxAbsDiff(outR, refR); d > 1e-12 {
		t.Errorf("conformance limiter R diverges by %g", d)
	}
}

// TestConformanceLimiterGuarantee: the streaming limiter's output true peak must
// not exceed the ceiling (the whole point of a conformance limiter).
func TestConformanceLimiterGuarantee(t *testing.T) {
	const sr = 44100
	const ceilingDB = -5.0
	l, r := testSignal(60000)
	lim := newConformanceLimiter(sr, ceilingDB, 20, 80)
	var outL, outR []float64
	for i := range l {
		if ol, or, ok := lim.push(l[i], r[i]); ok {
			outL = append(outL, ol)
			outR = append(outR, or)
		}
	}
	lim.flush(func(a, b float64) { outL = append(outL, a); outR = append(outR, b) })

	// Measure true peak of the output the same way the meter does.
	var hL, hR tpHist
	var maxTP float64
	for i := range outL {
		hL.push(outL[i])
		hR.push(outR[i])
		if mx := interpMax(&hL, &hR); mx > maxTP {
			maxTP = mx
		}
	}
	ceiling := dbToLin(ceilingDB)
	if maxTP > ceiling*(1+1e-6) {
		t.Errorf("output true peak %.4f dBTP exceeds ceiling %.1f dBTP", 20*math.Log10(maxTP), ceilingDB)
	}
}

func TestMeasureChainSilence(t *testing.T) {
	var buf bytes.Buffer
	writeF32LE(&buf, make([]float32, 44100*2*2)) // 2 s stereo silence
	m, err := MeasureChain(&buf, 44100)
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsInf(m.IntegratedLUFS, -1) {
		t.Errorf("silence integrated LUFS = %v, want -Inf", m.IntegratedLUFS)
	}
}

func TestMeasureChainSineLoudness(t *testing.T) {
	// −23 dBFS 997 Hz sine, channel-summed BS.1770: a stereo (dual-mono) tone
	// reads ~3 LU above the single-channel figure, and K-weighting near 1 kHz is
	// close to flat, so expect roughly −20 LUFS. Generous bounds.
	const sr = 44100
	amp := math.Pow(10, -23.0/20)
	n := sr * 8
	buf := make([]float32, n*2)
	for i := 0; i < n; i++ {
		v := float32(amp * math.Sin(2*math.Pi*997*float64(i)/sr))
		buf[i*2] = v
		buf[i*2+1] = v
	}
	var b bytes.Buffer
	writeF32LE(&b, buf)
	m, err := MeasureChain(&b, sr)
	if err != nil {
		t.Fatal(err)
	}
	if m.IntegratedLUFS < -24 || m.IntegratedLUFS > -16 {
		t.Errorf("sine integrated LUFS = %.2f, want roughly [-24,-16]", m.IntegratedLUFS)
	}
}

// TestConvergeEndToEnd: a quiet, dynamic signal through the converging chain
// lands near the target LUFS and under the true-peak ceiling.
func TestConvergeEndToEnd(t *testing.T) {
	const sr = 44100
	l, r := testSignal(sr * 6)
	for i := range l { // scale down so it needs positive gain
		l[i] *= 0.2
		r[i] *= 0.2
	}
	inter := make([]float32, len(l)*2)
	for i := range l {
		inter[i*2] = float32(l[i])
		inter[i*2+1] = float32(r[i])
	}
	var srcBuf bytes.Buffer
	writeF32LE(&srcBuf, inter)

	final, _, _ := convergeChain(t, sr, srcBuf.Bytes(), -18, -5, 7)
	if math.Abs(final.IntegratedLUFS-(-18.0)) > 1.5 {
		t.Errorf("final LUFS = %.2f, want within 1.5 of -18", final.IntegratedLUFS)
	}
	if final.TruePeakDB > -5.0+0.1 {
		t.Errorf("final true peak = %.2f dBTP, exceeds -5 ceiling", final.TruePeakDB)
	}
}
