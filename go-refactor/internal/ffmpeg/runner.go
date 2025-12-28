package ffmpeg

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/fremen-fi/tnt/go/platform"
)

var Path string

func init() {
	Path = extractFFmpeg()
}

// extractFFmpeg writes the embedded FFmpeg binary to a temp location and returns the path
func extractFFmpeg() string {
	tmpDir := os.TempDir()

	var name string
	if runtime.GOOS == "windows" {
		name = "ffmpeg.exe"
	} else {
		name = "ffmpeg"
	}

	ffmpegPath := filepath.Join(tmpDir, name)
	os.WriteFile(ffmpegPath, platform.FFmpegBinary, 0755)
	return ffmpegPath
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
