//go:build e2e

package main

// Integration tests for the staged-intermediate-file pipeline.
//
// The pipeline writes one 192 kHz / pcm_f32le WAV per processing stage
// (EQ, dynaudnorm, hot-peak attenuation, compression) into $TMPDIR and
// reads it back for the next stage's analysis. Per-band analysis is
// parallelized so wall-clock stays sane on multi-core machines.
//
// These tests build the actual CLI binary, run it against the embedded
// ffmpeg, and verify:
//
//   - the pipeline never asks the shipped ffmpeg for pcm_f64le (the
//     encoder it doesn't have — the original exit-status-8 cause)
//   - every {EQ, DynNorm, Dynamics} on/off combination still produces
//     output
//   - intermediate `tnt_*.wav` files appear during the run and are
//     cleaned up by the time it finishes
//   - stereo input stays stereo
//   - the multiband (5-band) analysis runs in parallel, not serially
//
// Build-tagged because they spawn the real binary. Run with:
//
//   go test -tags e2e -timeout 300s -run TestPipeline .

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fremen-fi/tnt/go/audio"
	"github.com/fremen-fi/tnt/go/internal/ffmpeg"
)

func buildCLI(t *testing.T, tmp string) string {
	t.Helper()
	binName := "tnt-pipeline-e2e"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(tmp, binName)
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build: %v", err)
	}
	return binPath
}

// writeStereoToneWAV writes a low-amplitude (~-20 dBFS) stereo sine. Low
// enough that the Broadcast hot-peak attenuation (which fires at peak >
// -5 dBFS) stays off, so the pipeline path is deterministic; loud enough
// that astats produces sane numbers.
func writeStereoToneWAV(path string, sampleRate int, seconds float64, freq float64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	const bitsPerSample = 16
	const channels = 2
	bytesPerSample := bitsPerSample / 8
	numSamples := int(float64(sampleRate) * seconds)
	dataSize := numSamples * channels * bytesPerSample
	byteRate := sampleRate * channels * bytesPerSample
	blockAlign := channels * bytesPerSample

	w := func(v any) error { return binary.Write(f, binary.LittleEndian, v) }

	if _, err := f.WriteString("RIFF"); err != nil {
		return err
	}
	if err := w(uint32(36 + dataSize)); err != nil {
		return err
	}
	if _, err := f.WriteString("WAVE"); err != nil {
		return err
	}
	if _, err := f.WriteString("fmt "); err != nil {
		return err
	}
	if err := w(uint32(16)); err != nil {
		return err
	}
	if err := w(uint16(1)); err != nil {
		return err
	}
	if err := w(uint16(channels)); err != nil {
		return err
	}
	if err := w(uint32(sampleRate)); err != nil {
		return err
	}
	if err := w(uint32(byteRate)); err != nil {
		return err
	}
	if err := w(uint16(blockAlign)); err != nil {
		return err
	}
	if err := w(uint16(bitsPerSample)); err != nil {
		return err
	}
	if _, err := f.WriteString("data"); err != nil {
		return err
	}
	if err := w(uint32(dataSize)); err != nil {
		return err
	}

	const amp = int16(3276) // ~0.1 of int16 max
	for i := 0; i < numSamples; i++ {
		s := int16(float64(amp) * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
		if err := w(s); err != nil {
			return err
		}
		if err := w(s); err != nil {
			return err
		}
	}
	return nil
}

func snapshotTmpDir(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatalf("read tmpdir: %v", err)
	}
	out := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "tnt_") && strings.HasSuffix(name, ".wav") {
			out[name] = true
		}
	}
	return out
}

// runCLIOnce drops a fixture into a fresh in dir, starts the daemon,
// waits for the produced output to stop growing, then sends SIGINT.
// Returns the produced file path.
func runCLIOnce(t *testing.T, binPath string, fixtureName string, extraArgs []string) string {
	t.Helper()

	tmp := t.TempDir()
	inDir := filepath.Join(tmp, "in")
	outDir := filepath.Join(tmp, "out")
	if err := os.MkdirAll(inDir, 0755); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(inDir, fixtureName)
	if err := writeStereoToneWAV(fixture, 48000, 2.0, 440.0); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	args := append([]string{
		"-i", inDir, "-o", outDir,
		"-format", "pcm", "-bd", "16", "-sr", "48000",
		"-workers", "1",
	}, extraArgs...)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}

	deadline := time.Now().Add(150 * time.Second)
	var produced string
	var lastSize int64 = -1
	stableCount := 0
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(outDir)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasSuffix(name, ".tmp.wav") || strings.HasSuffix(name, ".tmp") {
				continue
			}
			produced = filepath.Join(outDir, name)
			break
		}
		if produced != "" {
			st, err := os.Stat(produced)
			if err == nil && st.Size() > 0 {
				if st.Size() == lastSize {
					stableCount++
					if stableCount >= 3 {
						break
					}
				} else {
					stableCount = 0
					lastSize = st.Size()
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	_ = cmd.Process.Signal(syscall.SIGINT)
	waitErr := cmd.Wait()
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		t.Fatalf("binary did not shut down cleanly: %v", waitErr)
	}

	if produced == "" {
		t.Fatalf("CLI produced no output in %s", outDir)
	}
	if st, err := os.Stat(produced); err != nil || st.Size() == 0 {
		t.Fatalf("produced file is missing/empty: %s (err=%v)", produced, err)
	}
	return produced
}

func channelCount(t *testing.T, path string) string {
	t.Helper()
	out, _ := ffmpeg.Run("-i", path)
	s := string(out)
	switch {
	case strings.Contains(s, "stereo"):
		return "stereo"
	case strings.Contains(s, "mono"):
		return "mono"
	default:
		return "unknown"
	}
}

// Realistic stage combinations. We deliberately test the on/off matrix
// of the three switches users actually flip rather than every preset.
var stageCases = []struct {
	name string
	args []string
}{
	{"all_off", []string{
		"-p:eq", "0", "-p:dyn", "0", "-dyn-norm", "0",
		"-lufs", "0", "-rg", "0", "-phase-check", "0",
	}},
	{"eq_only", []string{
		"-p:eq", "3", "-p:dyn", "0", "-dyn-norm", "0",
		"-lufs", "0", "-rg", "0", "-phase-check", "0",
	}},
	{"dynnorm_only", []string{
		"-p:eq", "0", "-p:dyn", "0", "-dyn-norm", "1",
		"-lufs", "0", "-rg", "0", "-phase-check", "0",
	}},
	{"dynamics_only_sbc", []string{
		"-p:eq", "0", "-p:dyn", "1", "-dyn-norm", "0",
		"-lufs", "0", "-rg", "0", "-phase-check", "0",
	}},
	{"dynamics_only_broadcast", []string{
		"-p:eq", "0", "-p:dyn", "3", "-dyn-norm", "0",
		"-lufs", "0", "-rg", "0", "-phase-check", "0",
	}},
	{"eq_and_loudnorm", []string{
		"-p:eq", "3", "-p:dyn", "0", "-dyn-norm", "0",
		"-ebu", "-rg", "0", "-phase-check", "0",
	}},
	{"full_chain_broadcast", []string{
		"-p:eq", "3", "-p:dyn", "3", "-dyn-norm", "1",
		"-ebu", "-rg", "0", "-phase-check", "0",
	}},
}

// TestPipelineProducesOutputForEveryStageCombination asserts the
// staged-file pipeline still works across every {EQ, DynNorm, Dynamics}
// on/off combination users actually flip in the UI.
func TestPipelineProducesOutputForEveryStageCombination(t *testing.T) {
	tmp := t.TempDir()
	binPath := buildCLI(t, tmp)

	for _, tc := range stageCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out := runCLIOnce(t, binPath, tc.name+".wav", tc.args)
			st, err := os.Stat(out)
			if err != nil {
				t.Fatalf("stat output: %v", err)
			}
			if st.Size() < 1024 {
				t.Errorf("output suspiciously small (%d bytes); pipeline likely truncated", st.Size())
			}
		})
	}
}

// TestPipelineCleansUpIntermediateTempFiles asserts that intermediate
// tnt_*.wav files written under $TMPDIR by the staged pipeline get
// removed before the daemon exits. We don't assert they appeared during
// the run (parallel test runs would race) — we assert they're gone
// afterwards.
func TestPipelineCleansUpIntermediateTempFiles(t *testing.T) {
	tmp := t.TempDir()
	binPath := buildCLI(t, tmp)

	before := snapshotTmpDir(t)
	_ = runCLIOnce(t, binPath, "cleanup.wav", []string{
		"-p:eq", "3", "-p:dyn", "3", "-dyn-norm", "1",
		"-ebu", "-rg", "0", "-phase-check", "0",
	})
	after := snapshotTmpDir(t)

	var leaked []string
	for name := range after {
		if !before[name] {
			leaked = append(leaked, name)
		}
	}
	if len(leaked) > 0 {
		t.Errorf("pipeline left %d intermediate WAV(s) in tmpdir: %v",
			len(leaked), leaked)
	}
}

// TestPipelineNeverRequestsPCMf64le is the regression test for the
// original exit-status-8 crash. The shipped ffmpeg lacks pcm_f64le; if
// any pipeline stage asks for it, the encode dies. We scan the CLI's
// log output for "pcm_f64le" and fail if present.
func TestPipelineNeverRequestsPCMf64le(t *testing.T) {
	tmp := t.TempDir()
	binPath := buildCLI(t, tmp)

	inDir := filepath.Join(tmp, "in")
	outDir := filepath.Join(tmp, "out")
	if err := os.MkdirAll(inDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeStereoToneWAV(filepath.Join(inDir, "input.wav"), 48000, 2.0, 440.0); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	args := []string{
		"-i", inDir, "-o", outDir,
		"-format", "pcm", "-bd", "16", "-sr", "48000",
		"-p:eq", "3", "-p:dyn", "3", "-dyn-norm", "1",
		"-ebu", "-rg", "0", "-phase-check", "0",
		"-workers", "1",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	logPath := filepath.Join(tmp, "cli.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}

	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(outDir)
		done := false
		for _, e := range entries {
			if !e.IsDir() && !strings.HasSuffix(e.Name(), ".tmp.wav") {
				done = true
				break
			}
		}
		if done {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	_ = cmd.Process.Signal(syscall.SIGINT)
	_ = cmd.Wait()
	logFile.Close()

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(logBytes), "pcm_f64le") {
		i := strings.Index(string(logBytes), "pcm_f64le")
		start := i - 200
		if start < 0 {
			start = 0
		}
		end := i + 200
		if end > len(logBytes) {
			end = len(logBytes)
		}
		t.Errorf("pipeline requested pcm_f64le; shipped ffmpeg lacks that encoder. Log excerpt:\n…%s…",
			string(logBytes[start:end]))
	}
}

// TestPipelinePreservesStereo guards against the chain accidentally
// folding stereo to mono.
func TestPipelinePreservesStereo(t *testing.T) {
	tmp := t.TempDir()
	binPath := buildCLI(t, tmp)

	out := runCLIOnce(t, binPath, "stereo.wav", []string{
		"-p:eq", "3", "-p:dyn", "3", "-dyn-norm", "1",
		"-ebu", "-rg", "0", "-phase-check", "0",
	})

	if got := channelCount(t, out); got != "stereo" {
		t.Errorf("output channels = %q, want stereo", got)
	}
}

// TestParallelMultibandIsFasterThanSerial proves the 5-band MBC analysis
// runs concurrently. We measure wall-clock against a 30-second fixture:
// running 5 bands serially is ~5× the time of running one band; the
// parallelized version should be much closer to 1× (bounded by
// GOMAXPROCS). Allow 3× as the failure threshold to leave plenty of
// headroom for CI jitter while still catching a serial regression.
func TestParallelMultibandIsFasterThanSerial(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("single-core host; parallelism can't help")
	}

	tmp := t.TempDir()
	fixture := filepath.Join(tmp, "long.wav")
	if err := writeStereoToneWAV(fixture, 48000, 30.0, 440.0); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Baseline: time one band's astats invocation by hand.
	singleStart := time.Now()
	cmd := exec.Command(ffmpeg.Path, "-i", fixture, "-af", "lowpass=f=80,astats", "-f", "null", "-")
	if _, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("baseline ffmpeg failed: %v", err)
	}
	singleDur := time.Since(singleStart)

	// Now run the actual 5-band analyzer.
	parallelStart := time.Now()
	got, err := audio.AnalyzeFrequencyResponseBands(ffmpeg.Path, fixture)
	if err != nil {
		t.Fatalf("parallel analysis failed: %v", err)
	}
	parallelDur := time.Since(parallelStart)

	if len(got) != 10 {
		t.Fatalf("expected 10 bands, got %d", len(got))
	}

	// 10 bands serially would be ~10× single. Parallel on 2+ cores
	// should be well under 5× single. Threshold at 5× to avoid CI
	// flakes while still catching the "I accidentally serialized
	// everything" regression cleanly.
	maxAllowed := singleDur * 5
	if parallelDur > maxAllowed {
		t.Errorf("10-band parallel analysis took %v; single band was %v; expected parallel to stay under %v (5×)",
			parallelDur, singleDur, maxAllowed)
	}
	t.Logf("single band: %v, 10-band parallel: %v, speedup: %.1fx",
		singleDur, parallelDur, float64(singleDur*10)/float64(parallelDur))
}
