package audio

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// writeTestWAV writes a minimal PCM WAV with the given channel count, bit
// depth (16 or 32-float) and interleaved samples in [-1, 1].
func writeTestWAV(t *testing.T, path string, channels int, sampleRate uint32, interleaved []float64) {
	t.Helper()
	const bits = 16
	blockAlign := channels * bits / 8
	dataSize := len(interleaved) * bits / 8

	buf := make([]byte, 0, 44+dataSize)
	u16 := func(v uint16) { buf = binary.LittleEndian.AppendUint16(buf, v) }
	u32 := func(v uint32) { buf = binary.LittleEndian.AppendUint32(buf, v) }

	buf = append(buf, "RIFF"...)
	u32(uint32(36 + dataSize))
	buf = append(buf, "WAVE"...)
	buf = append(buf, "fmt "...)
	u32(16)
	u16(1) // PCM int
	u16(uint16(channels))
	u32(sampleRate)
	u32(sampleRate * uint32(blockAlign))
	u16(uint16(blockAlign))
	u16(bits)
	buf = append(buf, "data"...)
	u32(uint32(dataSize))
	for _, s := range interleaved {
		u16(uint16(int16(math.Round(s * 32767))))
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReadStereoWAV_mono verifies a mono WAV decodes into two independent,
// identical channel buffers — so every stereo-linked processor treats mono
// input exactly like dual-mono stereo.
func TestReadStereoWAV_mono(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mono.wav")
	mono := []float64{0, 0.25, -0.5, 1, -1, 0.125}
	writeTestWAV(t, path, 1, 48000, mono)

	left, right, sr, err := ReadStereoWAV(path)
	if err != nil {
		t.Fatalf("ReadStereoWAV(mono): %v", err)
	}
	if sr != 48000 {
		t.Fatalf("sample rate = %d, want 48000", sr)
	}
	if len(left) != len(mono) || len(right) != len(mono) {
		t.Fatalf("lengths = %d/%d, want %d", len(left), len(right), len(mono))
	}
	for i := range mono {
		if math.Abs(left[i]-mono[i]) > 1.0/32000 {
			t.Fatalf("left[%d] = %f, want ~%f", i, left[i], mono[i])
		}
		if left[i] != right[i] {
			t.Fatalf("channels differ at %d: %f vs %f", i, left[i], right[i])
		}
	}

	// Buffers must be independent — in-place processors write both.
	left[0] = 0.9
	if right[0] == 0.9 {
		t.Fatal("left and right share backing memory")
	}
}

// TestReadStereoWAV_stereo guards the unchanged stereo path through the
// centralized decoder.
func TestReadStereoWAV_stereo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stereo.wav")
	interleaved := []float64{0.5, -0.5, 0.25, -0.25, 1, -1}
	writeTestWAV(t, path, 2, 44100, interleaved)

	left, right, sr, err := ReadStereoWAV(path)
	if err != nil {
		t.Fatalf("ReadStereoWAV(stereo): %v", err)
	}
	if sr != 44100 {
		t.Fatalf("sample rate = %d, want 44100", sr)
	}
	want := [][2]float64{{0.5, -0.5}, {0.25, -0.25}, {1, -1}}
	if len(left) != len(want) {
		t.Fatalf("frames = %d, want %d", len(left), len(want))
	}
	for i, w := range want {
		if math.Abs(left[i]-w[0]) > 1.0/32000 || math.Abs(right[i]-w[1]) > 1.0/32000 {
			t.Fatalf("frame %d = %f/%f, want %f/%f", i, left[i], right[i], w[0], w[1])
		}
	}
}

// TestReduceLRASamples_matchesPath pins the samples-resident ReduceLRA to the
// path-based wrapper: same input, same result, same processed audio.
func TestReduceLRASamples_matchesPath(t *testing.T) {
	const (
		sr     = 8000
		secs   = 30
		loudA  = 0.5
		quietA = 0.05
	)
	// Alternate 3 s loud / 3 s quiet sine — wide LRA by construction.
	n := sr * secs
	left := make([]float64, n)
	right := make([]float64, n)
	for i := range n {
		amp := loudA
		if (i/(3*sr))%2 == 1 {
			amp = quietA
		}
		// Quantize to float32 up front: the path-based run reads its input
		// back from a float32 WAV, so the in-memory run must start from the
		// same quantized data for the two to be bit-identical.
		v := float64(float32(amp * math.Sin(2*math.Pi*440*float64(i)/sr)))
		left[i] = v
		right[i] = v
	}

	pathLeft := append([]float64(nil), left...)
	pathRight := append([]float64(nil), right...)
	path := filepath.Join(t.TempDir(), "lra.wav")
	if err := WriteFloat32WAV(path, sr, pathLeft, pathRight); err != nil {
		t.Fatal(err)
	}

	gotPath, err := ReduceLRA(path, 7, 2)
	if err != nil {
		t.Fatalf("ReduceLRA: %v", err)
	}
	gotSamples := ReduceLRASamples(left, right, sr, 7, 2)

	if math.Abs(gotPath-gotSamples) > 1e-9 {
		t.Fatalf("path LRA %f != samples LRA %f", gotPath, gotSamples)
	}

	outL, outR, _, err := ReadStereoWAV(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i += 997 { // spot-check, float32 round-trip tolerance
		if math.Abs(outL[i]-left[i]) > 1e-6 || math.Abs(outR[i]-right[i]) > 1e-6 {
			t.Fatalf("processed audio diverges at %d: file %g/%g vs samples %g/%g",
				i, outL[i], outR[i], left[i], right[i])
		}
	}
}
