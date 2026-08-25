package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/fremen-fi/tnt/go/internal/telemetry"
)

// ----- File queue -----

func (n *AudioNormalizer) GetFiles() []string {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	out := make([]string, len(n.files))
	copy(out, n.files)
	return out
}

func (n *AudioNormalizer) SelectFiles() []string {
	paths, err := wailsruntime.OpenMultipleFilesDialog(n.ctx, wailsruntime.OpenDialogOptions{
		Title: "Select Audio or Video Files",
		Filters: []wailsruntime.FileFilter{
			{
				DisplayName: "Audio Files",
				Pattern:     "*.mp3;*.wav;*.flac;*.m4a;*.aac;*.ogg;*.opus;*.aiff;*.aif;*.ape",
			},
			{
				DisplayName: "Video Files",
				Pattern:     "*.mp4;*.mov;*.mkv;*.avi;*.webm;*.m4v;*.mpg;*.mpeg;*.wmv;*.flv;*.ts;*.3gp",
			},
		},
	})
	if err != nil {
		n.appLog.Write(fmt.Sprintf("Failed to open file dialog: %v", err))
		return n.GetFiles()
	}

	for _, p := range paths {
		n.addFile(p)
	}
	n.batchMode = false

	files := n.GetFiles()
	wailsruntime.EventsEmit(n.ctx, "file:added", files)
	return files
}

func (n *AudioNormalizer) SelectFolder() []string {
	dir, err := wailsruntime.OpenDirectoryDialog(n.ctx, wailsruntime.OpenDialogOptions{
		Title: "Select Folder",
	})
	if err != nil || dir == "" {
		return n.GetFiles()
	}

	n.inputDir = dir
	n.batchMode = true

	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if isMediaFile(path) {
			n.addFile(path)
		}
		return nil
	})
	if walkErr != nil {
		n.appLog.Write(fmt.Sprintf("Failed to scan folder: %v", walkErr))
	}

	files := n.GetFiles()
	wailsruntime.EventsEmit(n.ctx, "file:added", files)
	return files
}

func (n *AudioNormalizer) SetOutputFolder() string {
	dir, err := wailsruntime.OpenDirectoryDialog(n.ctx, wailsruntime.OpenDialogOptions{
		Title: "Select Output Folder",
	})
	if err != nil || dir == "" {
		return n.outputDir
	}
	n.outputDir = dir
	return n.outputDir
}

func (n *AudioNormalizer) GetOutputFolder() string {
	return n.outputDir
}

func (n *AudioNormalizer) RemoveFile(index int) []string {
	n.mutex.Lock()
	if index >= 0 && index < len(n.files) {
		n.files = append(n.files[:index], n.files[index+1:]...)
	}
	n.mutex.Unlock()

	files := n.GetFiles()
	wailsruntime.EventsEmit(n.ctx, "file:added", files)
	return files
}

func (n *AudioNormalizer) ClearFiles() []string {
	n.mutex.Lock()
	n.files = n.files[:0]
	n.mutex.Unlock()

	wailsruntime.EventsEmit(n.ctx, "file:added", []string{})
	return []string{}
}

func (n *AudioNormalizer) AddFiles(paths []string) []string {
	for _, p := range paths {
		if isMediaFile(p) {
			n.addFile(p)
		}
	}
	files := n.GetFiles()
	wailsruntime.EventsEmit(n.ctx, "file:added", files)
	return files
}

// ----- Processing -----

func (n *AudioNormalizer) applyConfig(cfg ProcessConfig) {
	n.format = cfg.Format
	n.sampleRate = cfg.SampleRate
	n.bitDepth = cfg.BitDepth
	n.bitrate = cfg.Bitrate
	n.useLoudnorm = cfg.UseLoudnorm
	n.customLoudnorm = cfg.CustomLoudnorm
	n.normalizeTarget = cfg.NormalizeTarget
	n.normalizeTargetTp = cfg.NormalizeTargetTp
	n.isSpeech = cfg.IsSpeech
	n.writeTags = cfg.WriteTags
	n.noTranscode = cfg.NoTranscode
	n.dataCompLevel = cfg.DataCompLevel
	n.dynamicsPreset = cfg.DynamicsPreset
	n.eqPreset = cfg.EqTarget
	n.bypassProc = cfg.BypassProc
	n.dynNorm = cfg.DynNorm
	n.phaseCheck = cfg.PhaseCheck
	n.videoAction = cfg.VideoAction
}

func (n *AudioNormalizer) Process(cfg ProcessConfig) {
	n.applyConfig(cfg)
	go n.process()
}

func (n *AudioNormalizer) PreviewSize(cfg ProcessConfig) {
	n.applyConfig(cfg)
	n.previewSize()
}

// ----- Watch mode -----

func (n *AudioNormalizer) StartWatching() bool {
	return n.startWatching()
}

func (n *AudioNormalizer) StopWatching() {
	n.stopWatching()
}

func (n *AudioNormalizer) SetInputFolder(dir string) string {
	n.inputDir = dir
	return n.inputDir
}

// ----- Metadata -----

func (n *AudioNormalizer) ReadMetadata(path string) (map[string]string, error) {
	return n.readMetadata(path)
}

func (n *AudioNormalizer) WriteMetadata(path string, tags map[string]string) error {
	return n.writeMetadataTags(path, tags)
}

func (n *AudioNormalizer) MetadataFields() []string {
	return metadataFields
}

// ----- Preferences -----

func (n *AudioNormalizer) LoadPreferences() Preferences {
	n.loadPreferences()
	return Preferences{
		AdvancedMode:          n.advancedMode,
		LastOutputDir:         n.outputDir,
		SimpleMode:            n.simplePreset,
		Format:                n.format,
		SampleRate:            n.sampleRate,
		BitDepth:              n.bitDepth,
		Bitrate:               n.bitrate,
		LoudnormEnabled:       n.useLoudnorm,
		CustomLoudnorm:        n.customLoudnorm,
		NormalizeTarget:       n.normalizeTarget,
		NormalizeTargetTp:     n.normalizeTargetTp,
		NormalizationStandard: n.normalizationStandard,
		DataCompLevel:         n.dataCompLevel,
		EqPreset:              n.eqPreset,
		DynPreset:             n.dynamicsPreset,
		DynNorm:               n.dynNorm,
		PhaseCheck:            n.phaseCheck,
		TelemetryEnabled:      n.telemetryEnabled,
	}
}

func (n *AudioNormalizer) SavePreferences(prefs Preferences) {
	n.advancedMode = prefs.AdvancedMode
	n.outputDir = prefs.LastOutputDir
	n.simplePreset = prefs.SimpleMode
	n.format = prefs.Format
	n.sampleRate = prefs.SampleRate
	n.bitDepth = prefs.BitDepth
	n.bitrate = prefs.Bitrate
	n.useLoudnorm = prefs.LoudnormEnabled
	n.customLoudnorm = prefs.CustomLoudnorm
	n.normalizeTarget = prefs.NormalizeTarget
	n.normalizeTargetTp = prefs.NormalizeTargetTp
	n.normalizationStandard = prefs.NormalizationStandard
	n.dataCompLevel = prefs.DataCompLevel
	n.eqPreset = prefs.EqPreset
	n.dynamicsPreset = prefs.DynPreset
	n.dynNorm = prefs.DynNorm
	n.phaseCheck = prefs.PhaseCheck
	n.telemetryEnabled = prefs.TelemetryEnabled
	if n.telemetry != nil {
		n.telemetry.SetEnabled(prefs.TelemetryEnabled)
	}
	n.savePreferences()
}

// SetTelemetryEnabled is a granular toggle for the Preferences UI; it does
// not require sending the full Preferences blob.
func (n *AudioNormalizer) SetTelemetryEnabled(enabled bool) {
	n.telemetryEnabled = enabled
	if n.telemetry != nil {
		n.telemetry.SetEnabled(enabled)
	}
	n.savePreferences()
}

// GetTelemetryEnabled returns the current opt-in state.
func (n *AudioNormalizer) GetTelemetryEnabled() bool {
	return n.telemetryEnabled
}

// ResetTelemetryID generates a new anonymous client ID. Used when the user
// wants to break linkability between past and future events.
func (n *AudioNormalizer) ResetTelemetryID() string {
	return telemetry.ResetClientID()
}

func (n *AudioNormalizer) ResetPreferences() {
	n.resetPreferences()
}

// ----- Misc -----

func (n *AudioNormalizer) GetVersion() string {
	return currentVersion
}

func (n *AudioNormalizer) GetOS() string {
	return runtime.GOOS
}

func (n *AudioNormalizer) CheckForUpdates() VersionInfo {
	var info VersionInfo
	done := make(chan struct{})
	go func() {
		checkForUpdates(currentVersion, n.logFile, func(v VersionInfo) {
			info = v
		})
		close(done)
	}()
	<-done
	return info
}

func (n *AudioNormalizer) SendLogReport() {
	n.sendLogReport()
}
