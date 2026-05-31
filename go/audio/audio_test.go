package audio

import (
	"math"
	"testing"
)

func approx(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func TestGetCompressionModifiers(t *testing.T) {
	cases := []struct {
		name        string
		ds          float64
		wantAttack  float64
		wantRelease float64
		wantRatio   float64
	}{
		{"very_compressed", 5.0, 4.0, 4.0, 0.15},
		{"moderate_compressed", 12.0, 2.0, 2.0, 2.1},
		{"normal_low", 15.0, 1.0, 1.0, 1.0},
		{"normal_high", 21.0, 1.0, 1.0, 1.0},
		{"highly_dynamic_just_above", 22.0, 0.0, 0.0, 0.0}, // checked separately
		{"highly_dynamic_max", 100.0, 1.0 / 4.0, 1.0 / 4.0, 8.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := GetCompressionModifiers(tc.ds)
			if tc.name == "highly_dynamic_just_above" {
				if m.AttackMultiplier >= 1.0 || m.RatioMultiplier <= 4.0 {
					t.Fatalf("ds=%v expected attack<1 and ratio>4, got %+v", tc.ds, m)
				}
				return
			}
			if !approx(m.AttackMultiplier, tc.wantAttack, 1e-6) ||
				!approx(m.ReleaseMultiplier, tc.wantRelease, 1e-6) ||
				!approx(m.RatioMultiplier, tc.wantRatio, 1e-6) {
				t.Fatalf("ds=%v got %+v want attack=%v release=%v ratio=%v", tc.ds, m, tc.wantAttack, tc.wantRelease, tc.wantRatio)
			}
		})
	}
}

func TestGetBaseRatioFromCrest(t *testing.T) {
	cases := []struct {
		crest float64
		want  float64
	}{
		{2.0, 1.4},
		{3.0, 1.4},
		{4.0, 2.0},
		{5.0, 2.0},
		{6.0, 4.0},
		{8.0, 4.0},
		{12.0, 6.0},
		{16.0, 8.0},
		{20.0, 8.0},
	}
	for _, tc := range cases {
		got := GetBaseRatioFromCrest(tc.crest)
		if !approx(got, tc.want, 1e-6) {
			t.Errorf("crest=%v got=%v want=%v", tc.crest, got, tc.want)
		}
	}
}

func TestClampCompressorParams(t *testing.T) {
	// Below mins clamp up
	th, r, a, rel, mu := ClampCompressorParams(0, 0, 0, 0, 0)
	if th < 0.00097562 || r < 1.0 || a < 0.01 || rel < 0.01 || mu < 1.0 {
		t.Fatalf("clamp-min failed: %v %v %v %v %v", th, r, a, rel, mu)
	}
	// Above maxes clamp down
	th, r, a, rel, mu = ClampCompressorParams(2.0, 99.0, 9999.0, 99999.0, 999.0)
	if th != 1.0 || r != 20.0 || a != 2000.0 || rel != 9000.0 || mu != 64.0 {
		t.Fatalf("clamp-max failed: %v %v %v %v %v", th, r, a, rel, mu)
	}
}

func TestGetKneeFromRatio(t *testing.T) {
	cases := []struct {
		r, want float64
	}{
		{0.5, 1.0}, {1.5, 2.0}, {3.0, 3.0}, {5.0, 4.0}, {10.0, 6.0}, {15.0, 7.5},
	}
	for _, tc := range cases {
		if got := GetKneeFromRatio(tc.r); !approx(got, tc.want, 1e-6) {
			t.Errorf("ratio=%v got=%v want=%v", tc.r, got, tc.want)
		}
	}
}

func TestDbLinearRoundTrip(t *testing.T) {
	for _, db := range []float64{-60, -23, -10, 0, 6} {
		lin := DbToLinear(db)
		got := LinearToDb(lin)
		if !approx(got, db, 1e-9) {
			t.Errorf("db=%v lin=%v back=%v", db, lin, got)
		}
	}
	if got := LinearToDb(0); got != -100.0 {
		t.Errorf("LinearToDb(0) = %v, want -100", got)
	}
	if got := LinearToDb(-1); got != -100.0 {
		t.Errorf("LinearToDb(-1) = %v, want -100 (clamp)", got)
	}
}

const sampleAstats = `
[Parsed_astats_0 @ 0x55] Channel: 1
[Parsed_astats_0 @ 0x55] Peak level dB: -3.012345
[Parsed_astats_0 @ 0x55] RMS peak dB: -10.987654
[Parsed_astats_0 @ 0x55] RMS trough dB: -45.111111
[Parsed_astats_0 @ 0x55] RMS level dB: -20.123456
[Parsed_astats_0 @ 0x55] Crest factor: 4.567890
[Parsed_astats_0 @ 0x55] Dynamic range: 25.678901
[Parsed_astats_0 @ 0x55] Noise floor dB: -78.432100
[Parsed_astats_0 @ 0x55] Channel: 2
[Parsed_astats_0 @ 0x55] Peak level dB: -3.234567
[Parsed_astats_0 @ 0x55] Overall
[Parsed_astats_0 @ 0x55] Peak level dB: -2.987654
[Parsed_astats_0 @ 0x55] RMS peak dB: -10.512345
[Parsed_astats_0 @ 0x55] RMS trough dB: -44.876543
[Parsed_astats_0 @ 0x55] RMS level dB: -19.456789
[Parsed_astats_0 @ 0x55] Noise floor dB: -77.123456
`

func TestParseAstatsOutput(t *testing.T) {
	a := ParseAstatsOutput(sampleAstats)
	if !approx(a.PeakLevel, -2.987654, 1e-4) {
		t.Errorf("PeakLevel = %v", a.PeakLevel)
	}
	if !approx(a.RMSPeak, -10.512345, 1e-4) {
		t.Errorf("RMSPeak = %v", a.RMSPeak)
	}
	if !approx(a.RMSLevel, -19.456789, 1e-4) {
		t.Errorf("RMSLevel = %v", a.RMSLevel)
	}
	if !approx(a.NoiseFloor, -77.123456, 1e-4) {
		t.Errorf("NoiseFloor = %v", a.NoiseFloor)
	}
	// Crest comes from the first match (Channel 1)
	if !approx(a.CrestFactor, 4.567890, 1e-4) {
		t.Errorf("CrestFactor = %v", a.CrestFactor)
	}
}

func TestParseAstatsEmpty(t *testing.T) {
	a := ParseAstatsOutput("nothing useful")
	if a == nil {
		t.Fatal("expected non-nil")
	}
	if a.PeakLevel != 0 || a.RMSLevel != 0 {
		t.Errorf("expected zero-value, got %+v", a)
	}
}

func TestParseDynamicsScore(t *testing.T) {
	d := ParseDynamicsScore(sampleAstats)
	if !approx(d.RMSPeak, -10.987654, 1e-4) {
		t.Errorf("RMSPeak = %v", d.RMSPeak)
	}
	if !approx(d.RMSLevel, -20.123456, 1e-4) {
		t.Errorf("RMSLevel = %v", d.RMSLevel)
	}
	if !approx(d.CrestFactor, 4.567890, 1e-4) {
		t.Errorf("CrestFactor = %v", d.CrestFactor)
	}
	// DS = sqrt(crest) * (peak - level)
	wantDS := math.Sqrt(4.567890) * (-10.987654 - -20.123456)
	if !approx(d.DynamicsScore, wantDS, 1e-4) {
		t.Errorf("DS = %v want %v", d.DynamicsScore, wantDS)
	}
}

func TestFrequencyBandFilters(t *testing.T) {
	got := FrequencyBandFilters()
	for _, k := range []string{"sub", "bass", "low_mid", "mid", "high"} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing band %q", k)
		}
	}
	if len(got) != 5 {
		t.Errorf("expected 5 bands, got %d", len(got))
	}
}

func TestCalculateMakeupGain(t *testing.T) {
	if g := CalculateMakeupGain(nil, -20, 4); g != 1.0 {
		t.Errorf("nil analysis should return 1.0, got %v", g)
	}
	a := &DynamicsAnalysis{RMSLevel: -10}
	if g := CalculateMakeupGain(a, -20, 1.0); g != 1.0 {
		t.Errorf("ratio=1 should return 1.0, got %v", g)
	}
	// Below threshold → no makeup
	a = &DynamicsAnalysis{RMSLevel: -30}
	if g := CalculateMakeupGain(a, -20, 4); g != 1.0 {
		t.Errorf("below threshold should return 1.0, got %v", g)
	}
	// Above threshold: positive makeup gain (>1)
	a = &DynamicsAnalysis{RMSLevel: -10}
	g := CalculateMakeupGain(a, -20, 4)
	if g <= 1.0 {
		t.Errorf("expected makeup > 1, got %v", g)
	}
}
