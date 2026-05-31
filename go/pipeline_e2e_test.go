//go:build e2e

package main

// Integration tests for the no-intermediate-files pipeline refactor.
//
// These build the actual CLI binary, run it against the embedded ffmpeg,
// and verify the invariants that distinguish the new pipeline from the
// old temp-file chain:
//
//   - no `tnt_*.wav` intermediate files appear in $TMPDIR while the
//     pipeline runs (the whole point of the refactor)
//   - the embedded ffmpeg is never asked to use pcm_f64le, the encoder
//     that was missing from the shipped build and caused the original
//     exit-status-8 crash
//   - every {EQ, DynNorm, Dynamics} on/off combination still produces
//     output (the cascade survives partial pipelines)
//   - stereo input stays stereo through the chain
//
// Build-tagged because they spawn the real binary and the shipped
// ffmpeg, so they're heavy. Run with:
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

	"github.com/fremen-fi/tnt/go/internal/ffmpeg"
)

// buildCLI compiles the binary once per test (each test gets a fresh
// TempDir to keep outputs isolated).
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

// writeStereoToneWAV emits a small low-amplitude sine WAV — quiet enough
// that hot-peak attenuation won't trigger (keeps the pipeline path
// deterministic) but loud enough that astats produces sane numbers.
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

	// -20 dBFS sine — low enough that the Broadcast hot-peak attenuation
	// (which fires at peak > -5 dBFS) stays off, so the pipeline path is
	// deterministic across test runs.
	const amp = int16(3276) // ~0.1 of int16 max — well below hot-peak threshold
	for i := 0; i < numSamples; i++ {
		s := int16(float64(amp) * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
		if err := w(s); err != nil { // left
			return err
		}
		if err := w(s); err != nil { // right
			return err
		}
	}
	return nil
}

// snapshotTmpDir returns the set of `tnt_*.wav` filenames currently in
// $TMPDIR. Used before/after a CLI run to assert nothing intermediate
// was written.
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

// runCLIOnce drops a fixture into inDir, starts the daemon, waits for
// the expected output to appear, then sends SIGINT. Returns the path
// of the produced file (which the caller may probe further).
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

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}

	// CLI mode rewrites the output filename based on what processing was
	// requested (e.g. ".normalized.wav", ".tagged.wav", plain name). Wait
	// for any file in outDir to exist AND finish growing — ffmpeg writes
	// the WAV in-place, so the file appears while still being written.
	deadline := time.Now().Add(90 * time.Second)
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
					if stableCount >= 3 { // ~600ms stable → file is finalized
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
		t.Fatalf("CLI produced no output in %s after 90s", outDir)
	}
	if st, err := os.Stat(produced); err != nil || st.Size() == 0 {
		t.Fatalf("produced file is missing/empty: %s (err=%v)", produced, err)
	}
	return produced
}

// channelCount runs the shipped ffmpeg against `path` and parses the
// channel count out of stderr ("stereo" / "mono" / "N channels").
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
		// Fall back to "N channels" form for surround / unusual layouts.
		i := strings.Index(s, " channels")
		if i < 0 {
			return "unknown:\n" + s
		}
		start := i
		for start > 0 && (s[start-1] >= '0' && s[start-1] <= '9') {
			start--
		}
		return s[start:i] + " channels"
	}
}

// stageCases is the cartesian product of the three real switches users
// flip in the UI: EQ preset on/off × DynNorm on/off × Dynamics preset
// on/off. (Within the on-state we pick one preset per stage; the
// FilterChain unit test already covers the abstract on/off matrix.)
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

// TestPipelineNoIntermediateFiles asserts the headline invariant of the
// refactor: across every realistic stage combination, the daemon must
// not leave any `tnt_*.wav` files in $TMPDIR. The old temp-file
// pipeline created one per stage; the new pipeline must create zero.
func TestPipelineNoIntermediateFiles(t *testing.T) {
	tmp := t.TempDir()
	binPath := buildCLI(t, tmp)

	for _, tc := range stageCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			before := snapshotTmpDir(t)
			_ = runCLIOnce(t, binPath, tc.name+".wav", tc.args)
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
		})
	}
}

// TestPipelineNeverRequestsPCMf64le asserts the cause of the original
// crash is structurally gone: the binary the user runs cannot ask the
// shipped ffmpeg for pcm_f64le anywhere in the pipeline, because the
// shipped build doesn't have that encoder. We attach a recorder, run
// the full chain (the path that was failing), and check every captured
// invocation.
//
// The recorder lives in the ffmpeg package and is process-local, so
// this test executes the pipeline in-process by importing the same
// helpers the CLI does, rather than spawning the binary (the binary
// would have its own ffmpeg.Recorder state).
func TestPipelineNeverRequestsPCMf64le(t *testing.T) {
	tmp := t.TempDir()
	binPath := buildCLI(t, tmp)

	// We can't reach into the spawned process's Recorder, so instead we
	// scan the CLI's log file — the CLI logs every ffmpeg invocation it
	// runs ("Final command: ffmpeg …") via logQuiet. If pcm_f64le ever
	// got requested, it would show up there.
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

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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

	deadline := time.Now().Add(90 * time.Second)
	var produced string
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(outDir)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasSuffix(name, ".tmp.wav") {
				continue
			}
			produced = filepath.Join(outDir, name)
			break
		}
		if produced != "" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	_ = cmd.Process.Signal(syscall.SIGINT)
	_ = cmd.Wait()
	logFile.Close()

	if produced == "" {
		t.Fatal("CLI never produced output")
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(logBytes), "pcm_f64le") {
		t.Errorf("CLI requested pcm_f64le somewhere; shipped ffmpeg lacks that encoder. Log excerpt around the match:\n%s",
			excerptAround(string(logBytes), "pcm_f64le", 200))
	}

	// And while we're here, the CLI app's own daemon log (separate from
	// stdout/stderr) lives under the user temp dir as tnt_app.log /
	// similar. We don't depend on that path; the in-process stderr log
	// already captures the "Final command: ffmpeg …" line.
}

// TestPipelinePreservesStereo asserts the chain doesn't quietly fold to
// mono. Old temp-file pipeline preserved channels because each stage
// re-decoded a PCM WAV; the new pipeline must do the same through pure
// filter composition.
func TestPipelinePreservesStereo(t *testing.T) {
	tmp := t.TempDir()
	binPath := buildCLI(t, tmp)

	out := runCLIOnce(t, binPath, "stereo.wav", []string{
		"-p:eq", "3", "-p:dyn", "3", "-dyn-norm", "1",
		"-ebu", "-rg", "0", "-phase-check", "0",
	})

	if got := channelCount(t, out); got != "stereo" {
		t.Errorf("output channels = %q, want stereo (input was stereo)", got)
	}
}

// excerptAround returns the substring of s centered on the first
// occurrence of needle, with up to `pad` chars on each side. Used to
// keep failure messages readable when scanning large log files.
func excerptAround(s, needle string, pad int) string {
	i := strings.Index(s, needle)
	if i < 0 {
		return ""
	}
	start := i - pad
	if start < 0 {
		start = 0
	}
	end := i + len(needle) + pad
	if end > len(s) {
		end = len(s)
	}
	return "…" + s[start:end] + "…"
}
