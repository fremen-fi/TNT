package normalizer

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"testing"
)

// sineTone generates interleaved f32 stereo sine samples at 440 Hz.
func sineTone(sampleRate, durationSec int, amplitudeLinear float64) []float32 {
	n := sampleRate * durationSec * 2 // stereo
	buf := make([]float32, n)
	for i := range sampleRate * durationSec {
		v := float32(amplitudeLinear * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate)))
		buf[i*2] = v
		buf[i*2+1] = v
	}
	return buf
}

func makeWAV(t *testing.T, sampleRate int, samples []float32) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "test-*.wav")
	if err != nil {
		t.Fatal(err)
	}
	dataBytes := int64(len(samples) * 4)
	if err := WriteWAVHeader(f, sampleRate, 2, dataBytes); err != nil {
		t.Fatal(err)
	}
	if err := writeF32LE(f, samples); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func TestMeasureLUFS_SilenceIsNegInf(t *testing.T) {
	samples := make([]float32, 44100*5*2) // 5 s silence
	path := makeWAV(t, 44100, samples)
	f, _ := os.Open(path)
	defer f.Close()
	info, _ := ReadWAVInfo(f)
	res, err := MeasureLUFS(f, info.SampleRate, info.DataSize)
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsInf(res.IntegratedLUFS, -1) {
		t.Errorf("silence: expected -Inf LUFS, got %v", res.IntegratedLUFS)
	}
}

func TestMeasureLUFS_SineReasonable(t *testing.T) {
	// 10 s of 440 Hz sine at -23 dBFS: LUFS should be around -23 ± 2.
	amp := math.Pow(10, -23.0/20)
	samples := sineTone(44100, 10, amp)
	path := makeWAV(t, 44100, samples)
	f, _ := os.Open(path)
	defer f.Close()
	info, _ := ReadWAVInfo(f)
	res, err := MeasureLUFS(f, info.SampleRate, info.DataSize)
	if err != nil {
		t.Fatal(err)
	}
	// Sine has a known relationship between amplitude and LUFS after K-weighting.
	// Tolerance ±3 LU covers the K-weighting attenuation at 440 Hz.
	if res.IntegratedLUFS < -30 || res.IntegratedLUFS > -18 {
		t.Errorf("LUFS %v out of expected range [-30, -18]", res.IntegratedLUFS)
	}
}

func TestNormalize_GainApplied(t *testing.T) {
	amp := math.Pow(10, -30.0/20) // quiet signal at -30 dBFS amplitude
	samples := sineTone(44100, 10, amp)
	src := makeWAV(t, 44100, samples)
	dst := src + ".out.wav"
	t.Cleanup(func() { os.Remove(dst) })

	cfg := Config{
		TargetLUFS:        -18.0,
		SamplePeakCeiling: math.Pow(10, -5.0/20),
		MaxLRA:            11,
	}
	res, err := Normalize(src, dst, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.AppliedGainDB <= 0 {
		t.Errorf("expected positive gain for quiet signal, got %v dB", res.AppliedGainDB)
	}

	// The output WAV should be readable.
	outF, err := os.Open(dst)
	if err != nil {
		t.Fatal("output file not created:", err)
	}
	defer outF.Close()
	outInfo, err := ReadWAVInfo(outF)
	if err != nil {
		t.Fatal("output WAV header invalid:", err)
	}
	if outInfo.SampleRate != 44100 || outInfo.Channels != 2 {
		t.Errorf("unexpected output format: %+v", outInfo)
	}
}

func TestReadWAVInfo_TruncatedRejected(t *testing.T) {
	// Build a WAV with a data chunk size at the RIFF limit.
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(0xFFFFFFFF))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(18))
	binary.Write(&buf, binary.LittleEndian, uint16(3)) // IEEE float
	binary.Write(&buf, binary.LittleEndian, uint16(2)) // channels
	binary.Write(&buf, binary.LittleEndian, uint32(44100))
	binary.Write(&buf, binary.LittleEndian, uint32(44100*8))
	binary.Write(&buf, binary.LittleEndian, uint16(8))
	binary.Write(&buf, binary.LittleEndian, uint16(32))
	binary.Write(&buf, binary.LittleEndian, uint16(0))
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(0xFFFFFFFF)) // clamped

	r := bytes.NewReader(buf.Bytes())
	_, err := ReadWAVInfo(r)
	if err == nil {
		t.Error("expected error for RIFF-limit data chunk, got nil")
	}
}

func TestApplyGainAndLimit_Clips(t *testing.T) {
	// Signal at 0 dBFS; gain of +6 dB should exceed ceiling of -5 dBFS.
	samples := []float32{0.9, 0.9, -0.9, -0.9} // 2 stereo frames
	var src bytes.Buffer
	writeF32LE(&src, samples)

	ceiling := math.Pow(10, -5.0/20) // 0.5623
	gain := math.Pow(10, 6.0/20)     // 2.0×
	var dst bytes.Buffer
	_, peakDB, err := ApplyGainAndLimit(&src, &dst, gain, ceiling)
	if err != nil {
		t.Fatal(err)
	}
	// Post-gain peak should be > ceiling (signal clipped).
	if peakDB <= 20*math.Log10(ceiling) {
		t.Errorf("expected peak above ceiling, got %v dB", peakDB)
	}
	// Output samples should be clamped.
	out := make([]float32, 4)
	binary.Read(bytes.NewReader(dst.Bytes()), binary.LittleEndian, &out)
	for _, s := range out {
		if math.Abs(float64(s)) > ceiling+1e-6 {
			t.Errorf("output sample %v exceeds ceiling %v", s, ceiling)
		}
	}
}
