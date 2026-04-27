package ffmpeg

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

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

// Command creates an exec.Cmd for FFmpeg with the given arguments
// It automatically applies platform-specific settings (like hiding console on Windows)
func Command(args ...string) *exec.Cmd {
	cmd := exec.Command(Path, args...)
	platform.HideWindow(cmd)
	return cmd
}

// Run executes FFmpeg with the given arguments and returns combined output
func Run(args ...string) ([]byte, error) {
	cmd := Command(args...)
	return cmd.CombinedOutput()
}
