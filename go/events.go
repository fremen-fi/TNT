package main

import "fmt"

// Phase 1 stubs — Phase 2c replaces these with runtime.EventsEmit/MessageDialog.

func (n *AudioNormalizer) logStatus(message string) {
	fmt.Print(message)
	if !endsWithNewline(message) {
		fmt.Println()
	}
}

func (n *AudioNormalizer) emitProgress(fraction float64) {
	_ = fraction
}

func (n *AudioNormalizer) emitDone() {}

func (n *AudioNormalizer) showConfirmDialog(title, message string) bool {
	fmt.Printf("[confirm] %s — %s (auto-yes in Phase 1)\n", title, message)
	return true
}

func endsWithNewline(s string) bool {
	return len(s) > 0 && s[len(s)-1] == '\n'
}
