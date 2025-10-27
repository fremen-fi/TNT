//go:build windows

package main

import (
	"log"
	"os/exec"
	"syscall"
)

func hideWindow(cmd *exec.Cmd) {
	log.Println("Hiding window for command")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
		CreationFlags: 0x08000000,	
	}
}