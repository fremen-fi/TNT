package ffmpeg

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fremen-fi/tnt/go/platform"
)

var Path string

func init() {
	Path = findFFmpeg()
}

func findFFmpeg() string {
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name = "ffmpeg.exe"
	}

	// 1. Explicit override for development (trusted as-is)
	if p := os.Getenv("FFMPEG_PATH"); p != "" {
		return p
	}

	// 2. Sidecar next to the executable (app bundle / installed binary).
	// Validate it has the required codecs before trusting it — a system ffmpeg
	// found alongside the installed binary (e.g. /usr/bin/ffmpeg) may lack
	// non-free encoders like libfdk_aac that standard distro builds omit.
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			candidate := filepath.Join(filepath.Dir(exe), name)
			if _, err := os.Stat(candidate); err == nil {
				if runtime.GOOS != "linux" || hasRequiredCodecs(candidate) {
					return candidate
				}
			}
		}
	}

	// 3. Fall back: extract embedded binary to temp (dev / go run)
	tmpPath := filepath.Join(os.TempDir(), name)
	os.WriteFile(tmpPath, platform.FFmpegBinary, 0755)
	return tmpPath
}

func hasRequiredCodecs(ffmpegPath string) bool {
	out, err := exec.Command(ffmpegPath, "-version").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "enable-libfdk-aac")
}

// Recorder, when set, is invoked after every ffmpeg invocation that runs
// through Run / RunCmd. main.go installs the telemetry hook here at startup;
// when nil (tests, CLI subcommands that don't need telemetry), capture is a
// no-op. Setting Recorder must happen before any ffmpeg calls.
var Recorder func(args []string, output []byte, exitOK bool, dur time.Duration)

// Command creates an exec.Cmd for FFmpeg with the given arguments
// It automatically applies platform-specific settings (like hiding console on Windows)
func Command(args ...string) *exec.Cmd {
	cmd := exec.Command(Path, args...)
	platform.HideWindow(cmd)
	return cmd
}

// Run executes FFmpeg with the given arguments and returns combined output.
// Records telemetry via Recorder if installed.
func Run(args ...string) ([]byte, error) {
	cmd := Command(args...)
	return RunCmd(cmd)
}

// RunCmd runs a pre-built ffmpeg command via CombinedOutput and records
// telemetry. Use this in place of `cmd.CombinedOutput()` for call sites that
// need to configure cmd before running (env, custom dir, etc.).
func RunCmd(cmd *exec.Cmd) ([]byte, error) {
	start := time.Now()
	out, err := cmd.CombinedOutput()
	if Recorder != nil {
		// Strip the program path; we only want the args list, not the full
		// command which would include /tmp/ffmpeg or similar absolute paths.
		args := cmd.Args
		if len(args) > 0 {
			args = args[1:]
		}
		Recorder(args, out, err == nil, time.Since(start))
	}
	return out, err
}
