//go:build e2e

package main

// E2E smoke test for CLI mode. Builds the actual binary, drops a synthetic WAV
// in the watch dir, and confirms an output file appears. We deliberately do
// NOT verify audio substance — only that the shipping binary enters CLI mode,
// runs ffmpeg, writes output, and shuts down on SIGINT. Run with:
//
//   go test -tags e2e -timeout 180s -run TestCLI .

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestCLIDaemonProducesOutput(t *testing.T) {
	tmp := t.TempDir()

	binName := "tnt-cli-smoke"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(tmp, binName)

	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build: %v", err)
	}

	inDir := filepath.Join(tmp, "in")
	outDir := filepath.Join(tmp, "out")
	if err := os.MkdirAll(inDir, 0755); err != nil {
		t.Fatal(err)
	}

	fixture := filepath.Join(inDir, "smoke.wav")
	if err := writeSilenceWAV(fixture, 8000, 1, 0.5); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath,
		"-i", inDir, "-o", outDir,
		"-format", "pcm", "-bd", "16", "-sr", "8000",
		"-lufs", "0", "-rg", "0", "-dyn-norm", "0",
		"-p:eq", "0", "-p:dyn", "0", "-phase-check", "0",
		"-workers", "1",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}

	expected := filepath.Join(outDir, "smoke.wav")
	deadline := time.Now().Add(60 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		if st, err := os.Stat(expected); err == nil && st.Size() > 0 {
			found = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	// Whether or not we found the output, shut down the daemon cleanly.
	_ = cmd.Process.Signal(syscall.SIGINT)
	waitErr := cmd.Wait()

	if !found {
		t.Fatalf("CLI produced no output at %s (binary exit: %v)", expected, waitErr)
	}

	// SIGINT exit is acceptable; only flag genuinely abnormal failures.
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		t.Fatalf("binary did not shut down cleanly: %v", waitErr)
	}
}

// writeSilenceWAV writes a minimal PCM s16le WAV of `seconds` of silence.
// Duplicated (intentionally simple) from internal/ffmpeg's e2e helper to keep
// this test self-contained.
func writeSilenceWAV(path string, sampleRate, channels int, seconds float64) error {
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
	_, err = f.Write(make([]byte, dataSize))
	return err
}
