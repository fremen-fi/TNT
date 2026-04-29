//go:build e2e

package ffmpeg_test

// E2E smoke tests: prove the embedded ffmpeg actually runs and can decode +
// transcode a real (synthetic) WAV. Build-tagged so unit-test runs stay fast.
//
//   go test -tags e2e ./internal/ffmpeg/...

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fremen-fi/tnt/go/internal/ffmpeg"
)

func TestFFmpegVersionRuns(t *testing.T) {
	out, err := ffmpeg.Run("-version")
	if err != nil {
		t.Fatalf("ffmpeg -version failed: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "ffmpeg version") {
		t.Fatalf("output missing 'ffmpeg version':\n%s", s)
	}
	// We bundle a non-free build with libfdk_aac on linux/macOS — verify it.
	if !strings.Contains(s, "enable-libfdk-aac") {
		t.Errorf("bundled ffmpeg lacks libfdk_aac; AAC encoding will fall back: %s", firstLine(s))
	}
}

func TestFFmpegTranscodesSilentWAV(t *testing.T) {
	tmp := t.TempDir()
	in := filepath.Join(tmp, "silence.wav")
	if err := writePCMSilence(in, 8000, 1, 1.0); err != nil {
		t.Fatalf("write input: %v", err)
	}

	// Decode the WAV through libavformat into FLAC — proves the binary has
	// both a working WAV demuxer and the FLAC encoder.
	outFile := filepath.Join(tmp, "out.flac")
	out, err := ffmpeg.Run("-y", "-i", in, "-c:a", "flac", outFile)
	if err != nil {
		t.Fatalf("ffmpeg transcode failed: %v\n%s", err, out)
	}
	st, err := os.Stat(outFile)
	if err != nil {
		t.Fatalf("expected output FLAC: %v", err)
	}
	if st.Size() == 0 {
		t.Fatal("output FLAC is empty")
	}
}

func TestFFmpegLoudnessFilterRuns(t *testing.T) {
	tmp := t.TempDir()
	in := filepath.Join(tmp, "silence.wav")
	if err := writePCMSilence(in, 48000, 2, 1.0); err != nil {
		t.Fatalf("write input: %v", err)
	}
	// Pass through loudnorm (the core normalization filter the app relies on)
	// and discard the encoded output. If loudnorm is missing or the binary is
	// broken, this fails.
	out, err := ffmpeg.Run("-y", "-i", in, "-af",
		"loudnorm=I=-23:TP=-1:LRA=7:print_format=summary",
		"-f", "null", "-")
	if err != nil {
		t.Fatalf("ffmpeg loudnorm failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Input Integrated") &&
		!strings.Contains(string(out), "loudnorm") {
		t.Errorf("loudnorm summary not in output: %s", out)
	}
}

// writePCMSilence writes a minimal PCM s16le WAV file of `seconds` of silence.
func writePCMSilence(path string, sampleRate, channels int, seconds float64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	const bitsPerSample = 16
	bytesPerSample := bitsPerSample / 8
	numSamples := int(float64(sampleRate) * seconds)
	dataSize := numSamples * channels * bytesPerSample
	byteRate := sampleRate * channels * bytesPerSample
	blockAlign := channels * bytesPerSample

	// RIFF header
	if _, err := f.WriteString("RIFF"); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(36+dataSize)); err != nil {
		return err
	}
	if _, err := f.WriteString("WAVE"); err != nil {
		return err
	}

	// fmt chunk
	if _, err := f.WriteString("fmt "); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(16)); err != nil { // PCM chunk size
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(1)); err != nil { // PCM format
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(channels)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(sampleRate)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(byteRate)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(blockAlign)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(bitsPerSample)); err != nil {
		return err
	}

	// data chunk
	if _, err := f.WriteString("data"); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(dataSize)); err != nil {
		return err
	}
	silence := make([]byte, dataSize)
	if _, err := f.Write(silence); err != nil {
		return err
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
