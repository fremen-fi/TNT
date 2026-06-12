package main

import (
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (n *AudioNormalizer) emitProgress(fraction float64) {
	if n.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(n.ctx, "progress:update", fraction)
}

func (n *AudioNormalizer) emitDone() {
	if n.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(n.ctx, "progress:done")
}

func (n *AudioNormalizer) showConfirmDialog(title, message string) bool {
	if n.ctx == nil {
		return true
	}
	result, err := wailsruntime.MessageDialog(n.ctx, wailsruntime.MessageDialogOptions{
		Type:          wailsruntime.QuestionDialog,
		Title:         title,
		Message:       message,
		Buttons:       []string{"Yes", "No"},
		DefaultButton: "Yes",
	})
	if err != nil {
		return false
	}
	return result == "Yes"
}
