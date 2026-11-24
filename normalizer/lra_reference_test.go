package normalizer

import (
	"bytes"
	"math"
	"testing"
)

// Reference tests for the streaming loudness-range measurement. Signals are
// generated in memory; expected values follow from the signal construction and
// EBU Tech 3342 (3 s short-term blocks sampled at >= 10 Hz).

// stereoSections builds dual-mono 1 kHz PCM from consecutive (dBFS, seconds) sections.
func stereoSections(sr int, secs ...[2]float64) []byte {
	var buf []float32
	for _, sec := range secs {
		amp := math.Pow(10, sec[0]/20)
		n := int(sec[1] * float64(sr))
		for i := 0; i < n; i++ {
			v := float32(amp * math.Sin(2*math.Pi*1000*float64(i)/float64(sr)))
			buf = append(buf, v, v)
		}
	}
	var b bytes.Buffer
	writeF32LE(&b, buf)
	return b.Bytes()
}

// Two long sections: LRA equals the level step between them (EBU 3342 tone-step
// cases). Checked through both the MeasureChain and MeasureLUFS entry points.
func TestLRAStepReference(t *testing.T) {
	for _, sr := range []int{44100, 48000} {
		for _, step := range []float64{5, 10, 15} {
			pcm := stereoSections(sr, [2]float64{-23, 30}, [2]float64{-23 - step, 30})

			m, err := MeasureChain(bytes.NewReader(pcm), sr)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(m.LRA-step) > 0.05 {
				t.Errorf("MeasureChain %d Hz, %.0f dB step: LRA = %.3f, want %.0f", sr, step, m.LRA, step)
			}

			res, err := MeasureLUFS(bytes.NewReader(pcm), sr, -1)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(res.LRA-step) > 0.05 {
				t.Errorf("MeasureLUFS %d Hz, %.0f dB step: LRA = %.3f, want %.0f", sr, step, res.LRA, step)
			}
		}
	}
}

// Both entry points must agree on the same material.
func TestLRAEntryPointsAgree(t *testing.T) {
	const sr = 44100
	pcm := stereoSections(sr, [2]float64{-20, 14}, [2]float64{-32, 14}, [2]float64{-26, 14})
	m, err := MeasureChain(bytes.NewReader(pcm), sr)
	if err != nil {
		t.Fatal(err)
	}
	res, err := MeasureLUFS(bytes.NewReader(pcm), sr, -1)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(m.LRA-res.LRA) > 1e-6 {
		t.Errorf("MeasureChain LRA %.6f != MeasureLUFS LRA %.6f", m.LRA, res.LRA)
	}
}

// Sensitivity to the short-term hop: with 3 s sections alternating by 15 dB, a
// 1 s hop aligns blocks with the sections and reads the full ~15.6 LU step. At
// the required 10 Hz hop most blocks straddle a boundary, so the 10th-95th
// percentile spread is much narrower (about 8.9 LU). This pins the hop rate.
func TestLRAShortTermHopIs10Hz(t *testing.T) {
	const sr = 44100
	var secs [][2]float64
	for i := 0; i < 8; i++ {
		lvl := -15.0
		if i%2 == 1 {
			lvl = -30.5
		}
		secs = append(secs, [2]float64{lvl, 3})
	}
	pcm := stereoSections(sr, secs...)
	m, err := MeasureChain(bytes.NewReader(pcm), sr)
	if err != nil {
		t.Fatal(err)
	}
	if m.LRA < 6 || m.LRA > 12 {
		t.Errorf("LRA = %.2f LU, want 6-12 (a 1 s hop would read ~15.5)", m.LRA)
	}
	if stepsPerSTBlock*100 != 3000 { // 30 steps x 100 ms = 3 s window, hopping one step
		t.Errorf("short-term window = %d ms, want 3000", stepsPerSTBlock*100)
	}
}

// Relative gate: a 30 LU-down section is excluded from LRA (EBU 3342, -20 LU gate).
func TestLRARelativeGateReference(t *testing.T) {
	const sr = 44100
	pcm := stereoSections(sr, [2]float64{-23, 30}, [2]float64{-28, 30}, [2]float64{-53, 6})
	m, err := MeasureChain(bytes.NewReader(pcm), sr)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(m.LRA-5) > 0.3 {
		t.Errorf("LRA = %.3f, want ~5 (-53 dBFS section is gated out)", m.LRA)
	}
}
