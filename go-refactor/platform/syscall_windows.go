//go:build windows

package platform

import (
	"log"
	"os/exec"
	"syscall"
)

// HideWindow configures a command to run without showing a console window on Windows
func HideWindow(cmd *exec.Cmd) {
	log.Println("Hiding window for command")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
}
