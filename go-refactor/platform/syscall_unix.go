//go:build !windows

package platform

import "os/exec"

// HideWindow is a no-op on non-Windows platforms
func HideWindow(cmd *exec.Cmd) {
	// No action needed on Unix-like systems
}
