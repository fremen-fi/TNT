package normalizer

import (
	"fmt"
	"math"
	"testing"
)

const rtSR = 48000.0

// ── helpers ─────────────────────────────────────────────────────────────────

func cloneSig(l, r []float64) ([]float64, []float64) {
	return append([]float64(nil), l...), append([]float64(nil), r...)
}

// forChunks calls fn on consecutive sub-slices of l/r, cycling through sizes.
func forChunks(l, r []float64, sizes []int, fn func(l, r []float64)) {
	n := len(l)
	for i, k := 0, 0; i < n; k++ {
		sz := sizes[k%len(sizes)]
		if i+sz > n {
			sz = n - i
		}
		fn(l[i:i+sz], r[i:i+sz])
		i += sz
	}
}

func runCompressor(c *Compressor, l, r []float64, sizes []int) ([]float64, []float64) {
	l, r = cloneSig(l, r)
	forChunks(l, r, sizes, c.Process)
	return l, r
}

// runLimiter pushes the stream through in chunks, drops the warm-up silence and
// appends the drained tail, yielding len(l) real output frames.
func runLimiter(lim *Limiter, l, r []float64, sizes []int) ([]float64, []float64) {
	l, r = cloneSig(l, r)
	forChunks(l, r, sizes, lim.Process)
	look := lim.LatencySamples()
	outL := append([]float64(nil), l[look:]...)
	outR := append([]float64(nil), r[look:]...)
	tl, tr := make([]float64, 64), make([]float64, 64)
	for lim.Pending() > 0 {
		n := lim.Drain(tl, tr)
		outL = append(outL, tl[:n]...)
		outR = append(outR, tr[:n]...)
	}
	return outL, outR
}

var blockPlans = [][]int{{1}, {7}, {128}, {1000}, {1, 2, 3, 500, 17, 128}}

func exactlyEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// rtSignal is testSignal plus a loud burst and a quiet tail so detectors and
// release both get exercised.
func rtSignal(n int) (l, r []float64) {
	l, r = testSignal(n)
	for i := n / 2; i < n/2+n/8 && i < n; i++ {
		l[i] *= 1.6
		r[i] *= 1.6
	}
	for i := n * 3 / 4; i < n; i++ {
		l[i] *= 0.05
		r[i] *= 0.05
	}
	return
}

var compressorCases = []struct {
	name string
	p    CompressorParams
}{
	{"rms-soft", CompressorParams{-24, 3, 6, 100, 1500, 0, 300}},
	{"peak-makeup", CompressorParams{-18, 4, 3, 5, 80, 4.5, 0}},
	{"limiter-inf", CompressorParams{-10, math.Inf(1), 6, 150, 1200, 0, 0}},
	{"hard-knee-fast", CompressorParams{-30, 2, 0, 1, 10, -3, 20}},
}

// ── Compressor ──────────────────────────────────────────────────────────────

func TestCompressorEquivalence(t *testing.T) {
	l, r := rtSignal(120000)
	for _, tc := range compressorCases {
		refL, refR := cloneSig(l, r)
		refProcessDynamics(refL, refR, rtSR, tc.p.ThresholdDb, tc.p.Ratio, tc.p.KneeDb,
			tc.p.AttackMs, tc.p.ReleaseMs, tc.p.MakeupDb, tc.p.RMSWindowMs)

		eng := newCompressor(rtSR, tc.p.ThresholdDb, tc.p.Ratio, tc.p.KneeDb,
			tc.p.AttackMs, tc.p.ReleaseMs, tc.p.MakeupDb, tc.p.RMSWindowMs)
		stepL, stepR := make([]float64, len(l)), make([]float64, len(l))
		for i := range l {
			stepL[i], stepR[i] = eng.step(l[i], r[i])
		}

		for _, plan := range blockPlans {
			name := fmt.Sprintf("%s/blocks=%v", tc.name, plan)
			outL, outR := runCompressor(NewCompressor(rtSR, tc.p), l, r, plan)
			if d := maxAbsDiff(outL, refL); d > 1e-12 {
				t.Errorf("%s: L differs from refProcessDynamics by %g", name, d)
			}
			if d := maxAbsDiff(outR, refR); d > 1e-12 {
				t.Errorf("%s: R differs from refProcessDynamics by %g", name, d)
			}
			if !exactlyEqual(outL, stepL) || !exactlyEqual(outR, stepR) {
				t.Errorf("%s: not bit-identical to the per-sample step path", name)
			}
		}
	}
}

func TestUpwardCompressorEquivalence(t *testing.T) {
	l, r := rtSignal(120000)
	for i := range l {
		l[i] *= 0.3
		r[i] *= 0.3
	}
	p := UpwardCompressorParams{-24, 3, 12, 300, 600, 5, 300}
	refL, refR := cloneSig(l, r)
	refUpwardCompress(refL, refR, rtSR, p.ThresholdDb, p.Ratio, p.KneeDb, p.AttackMs, p.ReleaseMs, p.MaxBoostDb, p.RMSWindowMs)
	for _, plan := range blockPlans {
		u := NewUpwardCompressor(rtSR, p)
		oL, oR := cloneSig(l, r)
		forChunks(oL, oR, plan, u.Process)
		if d := maxAbsDiff(oL, refL); d > 1e-12 {
			t.Errorf("blocks=%v: L differs by %g", plan, d)
		}
		if d := maxAbsDiff(oR, refR); d > 1e-12 {
			t.Errorf("blocks=%v: R differs by %g", plan, d)
		}
	}
}

func TestCompressorMonoEqualsStereo(t *testing.T) {
	l, _ := rtSignal(60000)
	for _, tc := range compressorCases {
		st := NewCompressor(rtSR, tc.p)
		mo := NewCompressor(rtSR, tc.p)
		sl, sr := cloneSig(l, l)
		forChunks(sl, sr, []int{128}, st.Process)
		m := append([]float64(nil), l...)
		for i := 0; i < len(m); i += 128 {
			mo.ProcessMono(m[i:min(i+128, len(m))])
		}
		if !exactlyEqual(sl, sr) {
			t.Errorf("%s: stereo with L==R produced L != R", tc.name)
		}
		if !exactlyEqual(m, sl) {
			t.Errorf("%s: mono output not bit-identical to stereo L==R", tc.name)
		}
		if st.GainReductionDB() != mo.GainReductionDB() {
			t.Errorf("%s: gain reduction differs: stereo %v mono %v", tc.name, st.GainReductionDB(), mo.GainReductionDB())
		}
	}
}

func poison(x []float64) []float64 {
	x = append([]float64(nil), x...)
	x[1000] = math.NaN()
	x[1001] = math.Inf(1)
	x[1002] = math.Inf(-1)
	for i := 3000; i < 3050; i++ {
		x[i] = math.NaN()
	}
	return x
}

func sanitizedCopy(x []float64) []float64 {
	x = append([]float64(nil), x...)
	for i, v := range x {
		x[i] = sanitizeSample(v)
	}
	return x
}

func allFinite(x []float64) bool {
	for _, v := range x {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func TestCompressorNaNInfSanitized(t *testing.T) {
	cleanL, cleanR := rtSignal(3 * 48000)
	badL, badR := poison(cleanL), poison(cleanR)
	// Fast time constants so the state forgets the zeroed poison within ~1 s.
	p := CompressorParams{-24, 3, 6, 1, 20, 0, 5}

	outL, outR := runCompressor(NewCompressor(rtSR, p), badL, badR, []int{128})
	if !allFinite(outL) || !allFinite(outR) {
		t.Fatal("output contains NaN/Inf")
	}
	// Exactly what the clean engine does with the poison replaced by 0.
	refL, refR := sanitizedCopy(badL), sanitizedCopy(badR)
	refProcessDynamics(refL, refR, rtSR, p.ThresholdDb, p.Ratio, p.KneeDb, p.AttackMs, p.ReleaseMs, p.MakeupDb, p.RMSWindowMs)
	if d := maxAbsDiff(outL, refL); d > 1e-12 {
		t.Errorf("L differs from sanitized-input reference by %g", d)
	}
	if d := maxAbsDiff(outR, refR); d > 1e-12 {
		t.Errorf("R differs from sanitized-input reference by %g", d)
	}
	// State not corrupted: after the poison clears the stream re-converges onto
	// the never-poisoned run.
	cl, cr := cloneSig(cleanL, cleanR)
	refProcessDynamics(cl, cr, rtSR, p.ThresholdDb, p.Ratio, p.KneeDb, p.AttackMs, p.ReleaseMs, p.MakeupDb, p.RMSWindowMs)
	tail := 48000
	if d := maxAbsDiff(outL[tail:], cl[tail:]); d > 1e-6 {
		t.Errorf("did not recover to clean reference: max diff %g", d)
	}
}

func TestCompressorRuntimeParamChange(t *testing.T) {
	l, r := rtSignal(4 * 48000)
	a := CompressorParams{-24, 3, 6, 1, 50, 0, 5}
	b := CompressorParams{-30, 6, 6, 1, 400, 2, 5}
	c := NewCompressor(rtSR, a)
	// Largest per-sample step the one-pole smoothing allows across both settings.
	maxStep := 1 - math.Min(
		math.Min(onePoleCoef(a.AttackMs, rtSR), onePoleCoef(a.ReleaseMs, rtSR)),
		math.Min(onePoleCoef(b.AttackMs, rtSR), onePoleCoef(b.ReleaseMs, rtSR)))

	prev := dbToLin(-c.GainReductionDB())
	var worst float64
	one := make([]float64, 1)
	for i := range l {
		switch i {
		case 48000:
			before := c.GainReductionDB()
			c.SetParams(b)
			if c.GainReductionDB() != before {
				t.Fatalf("SetParams changed gain state: %v -> %v", before, c.GainReductionDB())
			}
		case 2 * 48000:
			c.SetParams(a)
		}
		one[0] = l[i]
		two := []float64{r[i]}
		c.Process(one, two)
		if math.IsNaN(one[0]) || math.IsInf(one[0], 0) {
			t.Fatalf("non-finite output at %d after param change", i)
		}
		g := dbToLin(-c.GainReductionDB())
		if d := math.Abs(g - prev); d > worst {
			worst = d
		}
		prev = g
	}
	if worst > maxStep+1e-12 {
		t.Errorf("gain stepped %.6f in one sample, one-pole bound is %.6f", worst, maxStep)
	}
}

func TestSettersClamp(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	bad := []CompressorParams{
		{nan, nan, nan, nan, nan, nan, nan},
		{inf, -inf, -5, -1, -1, inf, -inf},
		{-inf, 0, inf, inf, inf, -inf, inf},
	}
	l, r := rtSignal(2000)
	for _, p := range bad {
		c := NewCompressor(rtSR, p)
		oL, oR := runCompressor(c, l, r, []int{128})
		if !allFinite(oL) || !allFinite(oR) || math.IsNaN(c.GainReductionDB()) {
			t.Errorf("params %+v poisoned the compressor", p)
		}
		c.SetParams(p)
		oL, _ = runCompressor(c, l, r, []int{128})
		if !allFinite(oL) {
			t.Errorf("SetParams(%+v) poisoned the compressor", p)
		}
	}
	for _, args := range [][4]float64{{rtSR, nan, nan, nan}, {rtSR, inf, -inf, inf}, {rtSR, -inf, 99999, -3}} {
		lim := NewLimiter(args[0], args[1], args[2], args[3])
		lim.SetCeiling(args[1])
		lim.SetRelease(args[3])
		bl, br := rtSignal(100000) // longer than the clamped 1 s lookahead
		oL, oR := runLimiter(lim, bl, br, []int{128})
		if !allFinite(oL) || !allFinite(oR) || lim.LatencySamples() < 1 {
			t.Errorf("limiter args %v poisoned the limiter", args)
		}
	}
}

// ── Limiter ─────────────────────────────────────────────────────────────────

func TestLimiterEquivalence(t *testing.T) {
	l, r := rtSignal(120000)
	for _, cfg := range []struct{ ceil, look, rel float64 }{{-5, 20, 80}, {-1, 5, 50}, {-12, 1.5, 300}} {
		refL, refR := cloneSig(l, r)
		refLookaheadLimit(refL, refR, rtSR, cfg.ceil, cfg.look, cfg.rel)

		// Bit-identical to the push/flush path Conform uses.
		cl := newConformanceLimiter(rtSR, cfg.ceil, cfg.look, cfg.rel)
		var pL, pR []float64
		for i := range l {
			if a, b, ok := cl.push(l[i], r[i]); ok {
				pL, pR = append(pL, a), append(pR, b)
			}
		}
		cl.flush(func(a, b float64) { pL, pR = append(pL, a), append(pR, b) })

		for _, plan := range blockPlans {
			name := fmt.Sprintf("%+v/blocks=%v", cfg, plan)
			lim := NewLimiter(rtSR, cfg.ceil, cfg.look, cfg.rel)
			outL, outR := runLimiter(lim, l, r, plan)
			if len(outL) != len(l) {
				t.Fatalf("%s: got %d frames, want %d", name, len(outL), len(l))
			}
			if d := maxAbsDiff(outL, refL); d > 1e-12 {
				t.Errorf("%s: L differs from refLookaheadLimit by %g", name, d)
			}
			if d := maxAbsDiff(outR, refR); d > 1e-12 {
				t.Errorf("%s: R differs from refLookaheadLimit by %g", name, d)
			}
			if !exactlyEqual(outL, pL) || !exactlyEqual(outR, pR) {
				t.Errorf("%s: not bit-identical to the push/flush path", name)
			}
		}
	}
}

func TestLimiterWarmupSilenceAndLength(t *testing.T) {
	l, r := rtSignal(5000)
	lim := NewLimiter(rtSR, -3, 5, 50)
	look := lim.LatencySamples()
	if want := int(math.Round(5.0 / 1000 * rtSR)); look != want {
		t.Fatalf("LatencySamples = %d, want %d", look, want)
	}
	oL, oR := cloneSig(l, r)
	lim.Process(oL, oR)
	for i := 0; i < look; i++ {
		if oL[i] != 0 || oR[i] != 0 {
			t.Fatalf("frame %d of warm-up is not silent", i)
		}
	}
	if oL[look] == 0 {
		t.Error("first real frame is zero")
	}
	if lim.Pending() != look {
		t.Errorf("Pending() = %d, want %d", lim.Pending(), look)
	}
}

func TestLimiterLatencyImpulse(t *testing.T) {
	for _, lookMs := range []float64{0.01, 1, 5, 20} {
		lim := NewLimiter(rtSR, -1, lookMs, 50)
		look := lim.LatencySamples()
		const at = 300
		n := at + look + 400
		l := make([]float64, n)
		l[at] = 0.5
		r := append([]float64(nil), l...)
		forChunks(l, r, []int{128}, lim.Process)
		for i := range l {
			want := 0.0
			if i == at+look {
				want = 0.5
			}
			if l[i] != want || r[i] != want {
				t.Fatalf("lookahead %gms (latency %d): frame %d = %v/%v, want %v", lookMs, look, i, l[i], r[i], want)
			}
		}
	}
}

func TestLimiterMonoEqualsStereo(t *testing.T) {
	l, _ := rtSignal(60000)
	st := NewLimiter(rtSR, -5, 20, 80)
	mo := NewLimiter(rtSR, -5, 20, 80)
	sl, sr := cloneSig(l, l)
	forChunks(sl, sr, []int{128}, st.Process)
	m := append([]float64(nil), l...)
	for i := 0; i < len(m); i += 128 {
		mo.ProcessMono(m[i:min(i+128, len(m))])
	}
	if !exactlyEqual(sl, sr) || !exactlyEqual(m, sl) {
		t.Error("limiter mono is not bit-identical to stereo with L==R")
	}
}

func TestLimiterNaNInfSanitized(t *testing.T) {
	cleanL, cleanR := rtSignal(3 * 48000)
	badL, badR := poison(cleanL), poison(cleanR)
	cfg := struct{ ceil, look, rel float64 }{-5, 5, 80}

	outL, outR := runLimiter(NewLimiter(rtSR, cfg.ceil, cfg.look, cfg.rel), badL, badR, []int{128})
	if !allFinite(outL) || !allFinite(outR) {
		t.Fatal("output contains NaN/Inf")
	}
	refL, refR := sanitizedCopy(badL), sanitizedCopy(badR)
	refLookaheadLimit(refL, refR, rtSR, cfg.ceil, cfg.look, cfg.rel)
	if d := maxAbsDiff(outL, refL); d > 1e-12 {
		t.Errorf("L differs from sanitized-input reference by %g", d)
	}
	if d := maxAbsDiff(outR, refR); d > 1e-12 {
		t.Errorf("R differs from sanitized-input reference by %g", d)
	}
	cl, cr := cloneSig(cleanL, cleanR)
	refLookaheadLimit(cl, cr, rtSR, cfg.ceil, cfg.look, cfg.rel)
	tail := 2 * 48000
	if d := maxAbsDiff(outL[tail:], cl[tail:]); d > 1e-9 {
		t.Errorf("did not recover to clean reference: max diff %g", d)
	}
}

func TestLimiterRuntimeParamChange(t *testing.T) {
	const blk = 128
	l, r := rtSignal(6 * 48000)
	lim := NewLimiter(rtSR, -3, 5, 80)
	oL, oR := cloneSig(l, r)

	const change = 48000 * 3 // multiple of blk
	newCeil := -12.0
	for i := 0; i < len(oL); i += blk {
		if i == change {
			lim.SetCeiling(newCeil)
			lim.SetRelease(200)
		}
		lim.Process(oL[i:i+blk], oR[i:i+blk])
	}
	if !allFinite(oL) || !allFinite(oR) {
		t.Fatal("non-finite output after parameter change")
	}
	newThresh := dbToLin(newCeil)
	// The sample-peak clamp follows the new ceiling from the very next frame...
	for i := change; i < len(oL); i++ {
		if math.Abs(oL[i]) > newThresh || math.Abs(oR[i]) > newThresh {
			t.Fatalf("frame %d: sample %.6f exceeds new ceiling %.6f", i, math.Max(math.Abs(oL[i]), math.Abs(oR[i])), newThresh)
		}
	}
	// ...and since frames already in the lookahead are re-evaluated against the
	// new ceiling, the true-peak guarantee holds from the change on (plus the
	// 12-tap history of pre-change output).
	from := change + 12
	if tp := detectorTruePeak(oL[from:], oR[from:]); tp > newThresh*(1+1e-9) {
		t.Errorf("true peak %.3f dB exceeds lowered ceiling %.1f dB", dB(tp), newCeil)
	}
	// Raising the ceiling again must not poison anything.
	lim.SetCeiling(0)
	tl, tr := cloneSig(l[:blk], r[:blk])
	lim.Process(tl, tr)
	if !allFinite(tl) || !allFinite(tr) {
		t.Error("non-finite output after raising ceiling")
	}
}

// ── limiter guarantee ───────────────────────────────────────────────────────

type namedSig struct {
	name string
	l, r []float64
}

func guaranteeSignals() []namedSig {
	var s []namedSig
	l, r := testSignal(60000)
	s = append(s, namedSig{"tnt-testSignal", l, r})
	l, r = rtSignal(60000)
	for i := range l {
		l[i] *= 1.5
		r[i] *= 1.5
	}
	s = append(s, namedSig{"tnt-testSignal+6dB", l, r})

	// Inter-sample-peak torture: ±full-scale pairs and an fs/2 burst at every
	// sub-sample offset, in silence.
	for off := 0; off < 8; off++ {
		x := make([]float64, 4096)
		x[1000+off], x[1001+off] = 1, -1
		y := make([]float64, 4096)
		for i := 0; i < 64; i++ {
			y[2000+off+i] = 1 - 2*float64(i&1)
		}
		s = append(s, namedSig{fmt.Sprintf("isp-pair/off%d", off), x, scaled(x, 0.9)})
		s = append(s, namedSig{fmt.Sprintf("isp-fs2-burst/off%d", off), y, scaled(y, -1)})
	}
	for _, ph := range []float64{0, math.Pi / 4, math.Pi / 2, 3 * math.Pi / 4} {
		x := sineAt(8192, 0.25, ph, 1)
		s = append(s, namedSig{fmt.Sprintf("sine-fs4/phase%.2f", ph), x, scaled(x, 1)})
	}

	// Silence -> full-scale step.
	step := make([]float64, 8000)
	for i := 4000; i < len(step); i++ {
		step[i] = 1
	}
	s = append(s, namedSig{"silence-to-fullscale-step", step, scaled(step, 1)})

	wn := whiteNoise(1<<15, 7)
	s = append(s, namedSig{"white-noise", wn, whiteNoise(1<<15, 8)})
	bl := bandLimitedNoise(1<<15, 9)
	s = append(s, namedSig{"bandlimited-noise", bl, bandLimitedNoise(1<<15, 10)})
	return s
}

func scaled(x []float64, g float64) []float64 {
	y := make([]float64, len(x))
	for i := range x {
		y[i] = x[i] * g
	}
	return y
}

func TestLimiterGuarantee(t *testing.T) {
	const ceilingDB = -3.0
	ceil := dbToLin(ceilingDB)
	var worstOvershoot float64 = math.Inf(-1)
	for _, sg := range guaranteeSignals() {
		lim := NewLimiter(rtSR, ceilingDB, 5, 80)
		outL, outR := runLimiter(lim, sg.l, sg.r, []int{128})

		var sp float64
		for i := range outL {
			sp = math.Max(sp, math.Max(math.Abs(outL[i]), math.Abs(outR[i])))
		}
		if sp > ceil {
			t.Errorf("%s: sample peak %.6f exceeds ceiling %.6f", sg.name, sp, ceil)
		}
		if det := detectorTruePeak(outL, outR); det > ceil*(1+1e-9) {
			t.Errorf("%s: detector true peak %.4f dB exceeds ceiling %.1f dB", sg.name, dB(det), ceilingDB)
		}

		// Reference (128-tap, 8x) true peak of the output. The detector's own
		// reading is bounded by the ceiling (checked above); what the reference
		// sees beyond that is the detector's error on this signal. Budgets are
		// fixed regression guards just above the measured worst cases (see
		// TestTruePeakDetectorError): program-like and sine material < 0.9 dB,
		// white noise < 1.6 dB, the fs/2 burst pathology < 3.5 dB.
		ref := refTruePeakStereo(outL, outR)
		over := dB(ref) - ceilingDB
		budget := 0.9
		switch {
		case sg.name == "white-noise":
			budget = 1.6
		case len(sg.name) >= 14 && sg.name[:14] == "isp-fs2-burst/":
			budget = 3.5
		}
		if over > budget {
			t.Errorf("%s: reference true peak %+.3f dB over ceiling exceeds budget %.1f dB", sg.name, over, budget)
		}
		worstOvershoot = math.Max(worstOvershoot, over)
		if over > 0 {
			t.Logf("%s: reference true peak overshoots the ceiling by %.3f dB", sg.name, over)
		}
	}
	t.Logf("worst reference true-peak overshoot across guarantee signals: %+.3f dB over a %.1f dBFS ceiling", worstOvershoot, ceilingDB)
}

// ── zero allocation ─────────────────────────────────────────────────────────

func TestZeroAlloc(t *testing.T) {
	l, r := rtSignal(128)
	src := append([]float64(nil), l...)
	srcR := append([]float64(nil), r...)
	l, r = cloneSig(src, srcR)

	cp := CompressorParams{-24, 3, 6, 5, 80, 2, 50}
	cp2 := CompressorParams{-18, 4, 3, 10, 120, 0, 0}
	comp := NewCompressor(rtSR, cp)
	up := NewUpwardCompressor(rtSR, UpwardCompressorParams{-24, 3, 12, 30, 60, 5, 50})
	lim := NewLimiter(rtSR, -3, 5, 80)
	toggle := false

	cases := []struct {
		name string
		fn   func()
	}{
		{"Compressor.Process", func() { copy(l, src); copy(r, srcR); comp.Process(l, r) }},
		{"Compressor.ProcessMono", func() { copy(l, src); comp.ProcessMono(l) }},
		{"Compressor.SetParams", func() {
			toggle = !toggle
			if toggle {
				comp.SetParams(cp2)
			} else {
				comp.SetParams(cp)
			}
		}},
		{"Compressor.GainReductionDB", func() { _ = comp.GainReductionDB() }},
		{"UpwardCompressor.Process", func() { copy(l, src); copy(r, srcR); up.Process(l, r) }},
		{"UpwardCompressor.ProcessMono", func() { copy(l, src); up.ProcessMono(l) }},
		{"Limiter.Process", func() { copy(l, src); copy(r, srcR); lim.Process(l, r) }},
		{"Limiter.ProcessMono", func() { copy(l, src); lim.ProcessMono(l) }},
		{"Limiter.SetCeiling", func() { toggle = !toggle; lim.SetCeiling(map[bool]float64{true: -6, false: -3}[toggle]) }},
		{"Limiter.SetRelease", func() { lim.SetRelease(60) }},
		{"Limiter.Drain", func() { copy(l, src); copy(r, srcR); lim.Process(l, r); lim.Drain(l, r) }},
		{"Limiter.DrainMono", func() { copy(l, src); lim.ProcessMono(l); lim.DrainMono(l) }},
		{"Limiter.Pending", func() { _ = lim.Pending() }},
		{"Limiter.Reset", func() { lim.Reset() }},
		{"Compressor.Reset", func() { comp.Reset() }},
	}
	for _, tc := range cases {
		if n := testing.AllocsPerRun(1000, tc.fn); n != 0 {
			t.Errorf("%s: %v allocs per call, want 0", tc.name, n)
		}
	}
}

// ── reset ───────────────────────────────────────────────────────────────────

func TestResetRestoresFreshState(t *testing.T) {
	l, r := rtSignal(20000)
	lim := NewLimiter(rtSR, -5, 5, 80)
	a1, a2 := runLimiter(lim, l, r, []int{128})
	lim.Reset()
	b1, b2 := runLimiter(lim, l, r, []int{128})
	if !exactlyEqual(a1, b1) || !exactlyEqual(a2, b2) {
		t.Error("limiter after Reset differs from a fresh one")
	}

	p := compressorCases[0].p
	c := NewCompressor(rtSR, p)
	c1, c2 := runCompressor(c, l, r, []int{128})
	c.Reset()
	d1, d2 := runCompressor(c, l, r, []int{128})
	if !exactlyEqual(c1, d1) || !exactlyEqual(c2, d2) {
		t.Error("compressor after Reset differs from a fresh one")
	}
}

// ── benchmarks ──────────────────────────────────────────────────────────────

func benchSignal() (l, r []float64) {
	l, r = testSignal(128)
	for i := range l {
		l[i] *= 1.3
		r[i] *= 1.3
	}
	return
}

func BenchmarkCompressorBlock128(b *testing.B) {
	srcL, srcR := benchSignal()
	l, r := cloneSig(srcL, srcR)
	c := NewCompressor(rtSR, CompressorParams{-24, 3, 6, 5, 80, 0, 50})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(l, srcL)
		copy(r, srcR)
		c.Process(l, r)
	}
}

func BenchmarkLimiterBlock128(b *testing.B) {
	srcL, srcR := benchSignal()
	l, r := cloneSig(srcL, srcR)
	lim := NewLimiter(rtSR, -3, 5, 80)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(l, srcL)
		copy(r, srcR)
		lim.Process(l, r)
	}
}

// ── regressions from review ─────────────────────────────────────────────────

// A lookahead shorter than the detector's reach (12 taps) would turn the limiter
// into a clipper; NewLimiter must raise it and the guarantee must hold.
func TestLimiterMinLookahead(t *testing.T) {
	const ceilingDB = -3.0
	ceil := dbToLin(ceilingDB)
	sigs := []namedSig{}
	l, r := rtSignal(60000)
	sigs = append(sigs, namedSig{"rtSignal", l, r})
	x := make([]float64, 4096)
	x[1000], x[1001] = 1, -1
	sigs = append(sigs, namedSig{"isp-pair", x, scaled(x, 1)})

	for _, lookMs := range []float64{math.Inf(-1), 0, 0.001, 0.05, 0.1, 0.2} {
		for _, sg := range sigs {
			lim := NewLimiter(rtSR, ceilingDB, lookMs, 80)
			if lim.LatencySamples() < 12 {
				t.Fatalf("lookahead %g ms gave latency %d < 12", lookMs, lim.LatencySamples())
			}
			oL, oR := runLimiter(lim, sg.l, sg.r, []int{128})
			if det := detectorTruePeak(oL, oR); det > ceil*(1+1e-9) {
				t.Errorf("%s, lookahead %g ms: detector true peak %.3f dB exceeds ceiling %.1f dB", sg.name, lookMs, dB(det), ceilingDB)
			}
		}
	}
}

// Lowering the ceiling while a loud burst is inside the lookahead must limit it
// smoothly, not flat-top it against the new ceiling.
func TestLimiterCeilingLoweredDoesNotClip(t *testing.T) {
	const blk = 128
	n := 48000
	x := make([]float64, n)
	for i := 20000; i < 20000+4*blk; i++ { // burst spanning the lookahead window
		x[i] = 0.9 * math.Sin(2*math.Pi*1000*float64(i)/rtSR)
	}
	lim := NewLimiter(rtSR, -1, 5, 80)
	oL, oR := cloneSig(x, x)
	const change = 20000 + 2*blk
	newCeil := -12.0
	thresh := dbToLin(newCeil)
	for i := 0; i < n; i += blk {
		if i == change {
			lim.SetCeiling(newCeil)
		}
		lim.Process(oL[i:i+blk], oR[i:i+blk])
	}
	clamped := 0
	for i := change; i < n; i++ {
		if math.Abs(oL[i]) == thresh {
			clamped++
		}
	}
	if clamped > 0 {
		t.Errorf("%d frames were hard-clamped at the new ceiling after SetCeiling", clamped)
	}
}

func TestCompressorMakeupSlewsWithoutClick(t *testing.T) {
	// Constant mid-level signal well under threshold: gain stays 1, so output is
	// input * makeup and any makeup step shows directly.
	p := CompressorParams{ThresholdDb: 0, Ratio: 2, MakeupDb: 0}
	c := NewCompressor(rtSR, p)
	x := make([]float64, 48000)
	for i := range x {
		x[i] = 0.1
	}
	out := append([]float64(nil), x...)
	c.ProcessMono(out[:1000])
	p.MakeupDb = 6
	c.SetParams(p)
	c.ProcessMono(out[1000:])

	maxStep := 0.0
	for i := 1; i < len(out); i++ {
		maxStep = math.Max(maxStep, math.Abs(out[i]-out[i-1]))
	}
	// An instant 6 dB step on 0.1 would be ~0.1; the 10 ms slew is far below.
	if maxStep > 0.002 {
		t.Errorf("makeup change stepped the output by %.5f per sample", maxStep)
	}
	want := 0.1 * dbToLin(6)
	if got := out[len(out)-1]; got != want {
		t.Errorf("makeup did not settle exactly: got %v want %v", got, want)
	}
	// A static compressor is untouched by the slew path (bit-identical to step).
	l, r := rtSignal(5000)
	a, _ := runCompressor(NewCompressor(rtSR, compressorCases[1].p), l, r, []int{128})
	eng := newCompressor(rtSR, -18, 4, 3, 5, 80, 4.5, 0)
	for i := range l {
		if v, _ := eng.step(l[i], r[i]); v != a[i] {
			t.Fatalf("static makeup path differs from step at %d", i)
		}
	}
}

func TestCompressorPeakToRMSToggleStartsClean(t *testing.T) {
	peak := CompressorParams{ThresholdDb: -24, Ratio: 3, KneeDb: 6, AttackMs: 1, ReleaseMs: 20}
	rms := peak
	rms.RMSWindowMs = 50

	l, _ := rtSignal(24000)
	toggled := NewCompressor(rtSR, peak)
	loud := append([]float64(nil), l...)
	toggled.ProcessMono(loud)                   // peak mode: meanSq not tracked
	toggled.ProcessMono(make([]float64, 48000)) // 1 s silence, gain recovers to 1
	toggled.SetParams(rms)

	fresh := NewCompressor(rtSR, rms)
	a, b := append([]float64(nil), l...), append([]float64(nil), l...)
	toggled.ProcessMono(a)
	fresh.ProcessMono(b)
	if d := maxAbsDiff(a, b); d > 1e-9 {
		t.Errorf("RMS mode after a peak-mode period differs from a fresh RMS compressor by %g (stale meanSq)", d)
	}
}

func TestBadSampleRateDoesNotPoison(t *testing.T) {
	l, r := rtSignal(5000)
	for _, sr := range []float64{math.NaN(), -48000, 0, math.Inf(1), math.Inf(-1), 1e12} {
		c := NewCompressor(sr, compressorCases[0].p)
		u := NewUpwardCompressor(sr, UpwardCompressorParams{-24, 3, 12, 30, 60, 5, 50})
		lim := NewLimiter(sr, -3, 5, 80)
		cl, cr := runCompressor(c, l, r, []int{128})
		ul, ur := cloneSig(l, r)
		u.Process(ul, ur)
		ll, lr := runLimiter(lim, l, r, []int{128})
		for name, ok := range map[string]bool{"compressor": allFinite(cl) && allFinite(cr), "upward": allFinite(ul) && allFinite(ur), "limiter": allFinite(ll) && allFinite(lr)} {
			if !ok {
				t.Errorf("sampleRate %v: %s produced non-finite output", sr, name)
			}
		}
	}
}

func TestUnequalLengthsNeverPassRawAudio(t *testing.T) {
	nan := math.NaN()
	lim := NewLimiter(rtSR, -3, 1, 80)
	l := []float64{nan, 2, 2, 2}
	r := []float64{2, 2}
	lim.Process(l, r)
	if l[2] != 0 || l[3] != 0 {
		t.Errorf("limiter passed through unprocessed tail: %v", l)
	}
	c := NewCompressor(rtSR, compressorCases[0].p)
	l = []float64{2, 2, 2, 2}
	r = []float64{2, 2, 2, 2, 2, 2}
	c.Process(l, r)
	if r[4] != 0 || r[5] != 0 {
		t.Errorf("compressor passed through unprocessed tail: %v", r)
	}
}

func TestNaNSettersKeepCurrentValues(t *testing.T) {
	nan := math.NaN()
	p := CompressorParams{-20, 4, 5, 7, 90, 3, 40}
	c := NewCompressor(rtSR, p)
	c.SetParams(CompressorParams{nan, nan, nan, nan, nan, nan, nan})
	if c.p != p {
		t.Errorf("NaN SetParams changed parameters: %+v -> %+v", p, c.p)
	}
	lim := NewLimiter(rtSR, -6, 5, 80)
	lim.SetCeiling(nan)
	lim.SetRelease(nan)
	if lim.ceilingDB != -6 || lim.releaseMs != 80 {
		t.Errorf("NaN limiter setters changed state: ceiling %v release %v", lim.ceilingDB, lim.releaseMs)
	}
}

func TestSettersClampToDocumentedRanges(t *testing.T) {
	inf := math.Inf(1)
	c := NewCompressor(rtSR, CompressorParams{ThresholdDb: 99, Ratio: 0.2, KneeDb: -5, AttackMs: -1, ReleaseMs: inf, MakeupDb: -inf, RMSWindowMs: -3})
	want := CompressorParams{ThresholdDb: maxThresholdDb, Ratio: 1, KneeDb: 0, AttackMs: 0, ReleaseMs: maxTimeMs, MakeupDb: -maxMakeupDb, RMSWindowMs: 0}
	if c.p != want {
		t.Errorf("clamped params = %+v, want %+v", c.p, want)
	}
	lim := NewLimiter(rtSR, 5, 5, -1)
	if lim.ceilingDB != 0 || lim.releaseMs != 0 {
		t.Errorf("limiter clamp: ceiling %v release %v", lim.ceilingDB, lim.releaseMs)
	}
	lim.SetCeiling(-inf)
	if lim.ceilingDB != minCeilingDb {
		t.Errorf("ceiling -Inf clamped to %v, want %v", lim.ceilingDB, minCeilingDb)
	}
}

func TestDrainMonoMatchesStereo(t *testing.T) {
	l, _ := rtSignal(5000)
	st := NewLimiter(rtSR, -3, 5, 80)
	mo := NewLimiter(rtSR, -3, 5, 80)
	sl, sr := cloneSig(l, l)
	st.Process(sl, sr)
	m := append([]float64(nil), l...)
	mo.ProcessMono(m)
	ts, tr := make([]float64, st.Pending()), make([]float64, st.Pending())
	tm := make([]float64, mo.Pending())
	if st.Drain(ts, tr) != len(ts) || mo.DrainMono(tm) != len(tm) || !exactlyEqual(ts, tm) || !exactlyEqual(ts, tr) {
		t.Error("DrainMono differs from Drain on L==R")
	}
}
