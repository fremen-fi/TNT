package audio

import (
	"fmt"
	"math"
	"testing"
)

// TestTruePeakLimiterOffset limits a spread of signals (including the fs/4
// worst-case for inter-sample peaks) at several ceilings and reports
// measuredTP − ceiling to 6 decimals. If the residual is a consistent fixed
// amount across signals/ceilings, we compensate the limiter by that constant.
func TestTruePeakLimiterOffset(t *testing.T) {
	const sr = 96000.0
	n := int(sr) // 1 s

	gen := func(freq, amp float64) []float64 {
		s := make([]float64, n)
		for i := range s {
			s[i] = amp * math.Sin(2*math.Pi*freq*float64(i)/sr)
		}
		return s
	}

	signals := []struct {
		name string
		data []float64
	}{
		{"sine24k@0.90", gen(24000, 0.90)}, // fs/4 — worst-case inter-sample peak
		{"sine24k@1.00", gen(24000, 1.00)},
		{"sine23k@0.95", gen(23000, 0.95)},
		{"sine19k@0.95", gen(19000, 0.95)},
		{"sine7k@0.99", gen(7000, 0.99)},
		{"sine997@0.99", gen(997, 0.99)},
	}
	ceilings := []float64{-0.5, -1.0, -2.0, -3.0}

	fmt.Printf("%-13s %9s %13s %13s\n", "signal", "ceiling", "measuredTP", "offset")
	var sum, cnt float64
	for _, s := range signals {
		for _, c := range ceilings {
			l := append([]float64(nil), s.data...)
			r := append([]float64(nil), s.data...)
			LookaheadLimitSamples(l, r, sr, c, 1, 30)
			m := truePeak(l, r)
			off := m - c
			fmt.Printf("%-13s %+9.3f %+13.6f %+13.6f\n", s.name, c, m, off)
			sum += off
			cnt++
			// Invariant: the limiter never lets our-metered true peak exceed the
			// ceiling. (It can be under, when the signal never reached it.)
			if m > c+1e-6 {
				t.Errorf("%s @ %.3f: true peak %.6f exceeds ceiling", s.name, c, m)
			}
		}
	}
	fmt.Printf("mean offset over %.0f cases: %+.6f dB\n", cnt, sum/cnt)
}
