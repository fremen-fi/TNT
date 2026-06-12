package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/fremen-fi/tnt/go/internal/ffmpeg"
)

// writeToneWAV writes a 16-bit stereo PCM WAV with a ~-20 dBFS 440 Hz sine —
// the minimal valid input for the tag-only pipeline (ebur128 measurement plus
// a -c copy render).
func writeToneWAV(t *testing.T, path string, sampleRate int, seconds float64) {
	t.Helper()
	const channels = 2
	numSamples := int(float64(sampleRate) * seconds)
	dataSize := numSamples * channels * 2

	var buf bytes.Buffer
	w := func(v any) { binary.Write(&buf, binary.LittleEndian, v) }
	buf.WriteString("RIFF")
	w(uint32(36 + dataSize))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	w(uint32(16))
	w(uint16(1)) // PCM
	w(uint16(channels))
	w(uint32(sampleRate))
	w(uint32(sampleRate * channels * 2))
	w(uint16(channels * 2))
	w(uint16(16))
	buf.WriteString("data")
	w(uint32(dataSize))
	amp := 0.1 * 32767.0
	for i := 0; i < numSamples; i++ {
		s := int16(amp * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate)))
		w(s)
		w(s)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

// Regression test: in no-transcode (tag-only) mode processFile used to run
// the native loudness chain (reduceLRA / Gain / CharacterLimit / conformLimit)
// on workingPath, which — with the resample stage skipped — was still the
// user's ORIGINAL input file, silently rewriting it in place as a 32-bit
// float WAV (and failing outright on non-WAV input). Tag-only mode must never
// alter the input audio: the output is a -c copy of the source with
// ReplayGain tags from a read-only ebur128 measurement.
func TestProcessFileNoTranscodeLeavesOriginalUntouched(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns ffmpeg")
	}
	ffmpegPath = ffmpeg.Path

	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.wav")
	writeToneWAV(t, src, 44100, 1.0)

	orig, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmp, "out")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatal(err)
	}

	n := &AudioNormalizer{
		logFile:   &LogIntoFile{},
		appLog:    &LogApp{},
		outputDir: outDir,
	}
	n.normalizationStandard = EBU
	cfg := ProcessConfig{
		WriteTags:   true,
		NoTranscode: true,
		Format:      "MPEG-II L3",
	}
	n.applyConfig(cfg)

	if !n.processFile(src, cfg) {
		t.Fatal("processFile returned false in tag-only (no-transcode) mode")
	}

	after, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(orig, after) {
		t.Fatalf("tag-only mode modified the original input file (size %d -> %d)", len(orig), len(after))
	}

	tagged := filepath.Join(outDir, "src.tagged.wav")
	if _, err := os.Stat(tagged); err != nil {
		t.Fatalf("expected tagged output at %s: %v", tagged, err)
	}
}
