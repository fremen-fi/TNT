package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"image/color"
	"io/fs"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/fsnotify/fsnotify"

	"github.com/fremen-fi/tnt/go/internal/audio"
	"github.com/fremen-fi/tnt/go/internal/config"
	"github.com/fremen-fi/tnt/go/internal/ffmpeg"
)

const currentVersion = "1.2.0"

type VersionInfo struct {
	Version      string `json:"version"`
	DownloadURL  string `json:"download_url"`
	ReleaseNotes string `json:"release_notes"`
}

type AudioNormalizer struct {
	window       fyne.Window
	fileList     *widget.List
	files        []string
	outputDir    string
	processBtn   *widget.Button
	progressBar  *widget.ProgressBar
	statusLog    *widget.Entry
	outputLabel  *widget.Label

	modeTabs    *container.AppTabs
	modeWarning *widget.Label

	// Mode toggle
	advancedMode bool
	modeToggle   *widget.Check

	// Simple mode
	simpleGroupButtons *widget.RadioGroup
	simpleGroup        *fyne.Container

	// Advanced mode
	formatSelect      *widget.Select
	sampleRate        *widget.Select
	bitDepth          *widget.Select
	bitrateEntry      *widget.Entry
	normalizeTarget   *widget.Entry
	normalizeTargetTp *widget.Entry
	advancedContainer *fyne.Container

	// Common
	loudnormCheck          *widget.Check
	loudnormCustomCheck    *widget.Check
	loudnormLabel          *widget.Label
	writeTagsLabel         *widget.Label
	normalizeTargetLabel   *widget.Label
	normalizeTargetLabelTp *widget.Label
	normalizationStandard  string
	IsSpeechCheck          *widget.Check
	writeTags              *widget.Check
	noTranscode            *widget.Check
	dataCompLevel          *widget.Slider

	// dynamics
	dynamicsLabel *widget.Label
	dynamicsDrop  *widget.Select
	EqLabel       *widget.Label
	EqDrop        *widget.Select
	dynNorm       *widget.Check
	dynNormLabel  *widget.Label
	bypassProc    *widget.Check

	multibandFilter string

	logFile *os.File

	// watchmode
	watchMode        *widget.Check
	watching         bool
	watcherStop      chan bool
	jobQueue         chan string
	inputDir         string
	watcherWarnLabel *widget.Label

	watcherMutex sync.Mutex

	// batch processing
	batchMode bool

	menuWindow fyne.Window
	menuMutex  sync.Mutex

	mutex sync.Mutex
}

func checkForUpdates(currentVersion string, window fyne.Window, logFile *os.File) {
	logToFile(logFile, "Starting update check...")
	time.Sleep(500 * time.Millisecond)

	logToFile(logFile, "Fetching version info from server...")
	resp, err := http.Get("https://software.collinsgroup.fi/tnt-version.json")
	if err != nil {
		logToFile(logFile, fmt.Sprintf("HTTP error: %v", err))
		return
	}
	defer resp.Body.Close()

	logToFile(logFile, "Parsing JSON...")
	var versionInfo VersionInfo
	if err := json.NewDecoder(resp.Body).Decode(&versionInfo); err != nil {
		logToFile(logFile, fmt.Sprintf("JSON decode error: %v", err))
		return
	}

	logToFile(logFile, fmt.Sprintf("Current: %s, Remote: %s", currentVersion, versionInfo.Version))
	comparison := compareVersions(versionInfo.Version, currentVersion)
	logToFile(logFile, fmt.Sprintf("Comparison result: %d", comparison))

	if comparison > 0 {
		logToFile(logFile, "Update available, showing dialog...")
		fyne.Do(func() {
			dialog.ShowConfirm(
				"Update Available",
				fmt.Sprintf("Version %s is available!\n\n%s", versionInfo.Version, versionInfo.ReleaseNotes),
				func(download bool) {
					if download {
						var cmd *exec.Cmd
						switch runtime.GOOS {
						case "windows":
							cmd = exec.Command("cmd", "/c", "start", versionInfo.DownloadURL)
						case "darwin":
							cmd = exec.Command("open", versionInfo.DownloadURL)
						case "linux":
							cmd = exec.Command("xdg-open", versionInfo.DownloadURL)
						}
						if cmd != nil {
							cmd.Start()
						}
					}
				},
				window,
			)
		})
	} else {
		logToFile(logFile, "Already up to date")
		fyne.Do(func() {
			dialog.ShowInformation("Up to date", "You're running the latest version :)", window)
		})
	}
}

func logToFile(logFile *os.File, message string) {
	if logFile != nil {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		logFile.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, message))
	}
}

func compareVersions(v1, v2 string) int {
	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")

	for i := 0; i < len(parts1) && i < len(parts2); i++ {
		n1, _ := strconv.Atoi(parts1[i])
		n2, _ := strconv.Atoi(parts2[i])
		if n1 > n2 {
			return 1
		}
		if n1 < n2 {
			return -1
		}
	}
	return len(parts1) - len(parts2)
}

func (n *AudioNormalizer) logToFile(logFile *os.File, message string) {
	logToFile(logFile, message)
}

func (n *AudioNormalizer) logStatus(message string) {
	fyne.Do(func() {
		current := n.statusLog.Text
		if current != "" {
			current += "\n"
		}
		n.statusLog.SetText(current + message)
	})
}

func isAudioFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	audioExts := []string{".mp3", ".wav", ".flac", ".m4a", ".aac", ".ogg", ".opus", ".wma", ".aiff", ".aif", ".ape"}

	for _, audioExt := range audioExts {
		if ext == audioExt {
			return true
		}
	}
	return false
}

// Apple-inspired theme
type appleTheme struct{}

func (a *appleTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if variant == theme.VariantDark {
		switch name {
		case theme.ColorNameBackground:
			return color.RGBA{R: 0x2f, G: 0x2f, B: 0x2f, A: 0xff}
		case theme.ColorNameButton:
			return color.RGBA{R: 0x14, G: 0x1e, B: 0x30, A: 0xff}
		case theme.ColorNameDisabledButton:
			return color.RGBA{R: 0x4a, G: 0x4a, B: 0x4a, A: 0xff}
		case theme.ColorNameForeground:
			return color.RGBA{R: 0xeb, G: 0xeb, B: 0xeb, A: 0xff}
		case theme.ColorNameHover:
			return color.RGBA{R: 0x3f, G: 0x3f, B: 0x3f, A: 0xff}
		case theme.ColorNameInputBackground:
			return color.RGBA{R: 0x1a, G: 0x1a, B: 0x1a, A: 0xff}
		case theme.ColorNameInputBorder:
			return color.RGBA{R: 0x4a, G: 0x4a, B: 0x4a, A: 0xff}
		case theme.ColorNamePlaceHolder:
			return color.RGBA{R: 0x99, G: 0x99, B: 0x99, A: 0xff}
		case theme.ColorNamePressed:
			return color.RGBA{R: 0x0f, G: 0x16, B: 0x24, A: 0xff}
		case theme.ColorNameSelection:
			return color.RGBA{R: 0x14, G: 0x1e, B: 0x30, A: 0x66}
		case theme.ColorNameMenuBackground:
			return color.RGBA{R: 0x2f, G: 0x2f, B: 0x2f, A: 0xff}
		case theme.ColorNameOverlayBackground:
			return color.RGBA{R: 0x2f, G: 0x2f, B: 0x2f, A: 0xff}
		case theme.ColorNameDisabled:
			return color.RGBA{R: 0x77, G: 0x77, B: 0x77, A: 0xff}
		default:
			return theme.DefaultTheme().Color(name, variant)
		}
	}

	// Light variant
	switch name {
	case theme.ColorNameBackground:
		return color.RGBA{R: 0xeb, G: 0xeb, B: 0xeb, A: 0xff}
	case theme.ColorNameButton:
		return color.RGBA{R: 0xde, G: 0x79, B: 0x7c, A: 0xff}
	case theme.ColorNameDisabledButton:
		return color.RGBA{R: 0xbb, G: 0xbb, B: 0xbb, A: 0xff}
	case theme.ColorNameForeground:
		return color.RGBA{R: 0x1d, G: 0x1d, B: 0x1f, A: 0xff}
	case theme.ColorNameHover:
		return color.RGBA{R: 0xd5, G: 0xd5, B: 0xd5, A: 0xff}
	case theme.ColorNameInputBackground:
		return color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	case theme.ColorNameInputBorder:
		return color.RGBA{R: 0xd1, G: 0xd1, B: 0xd6, A: 0xff}
	default:
		return theme.DefaultTheme().Color(name, variant)
	}
}

func (a *appleTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (a *appleTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (a *appleTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}

func (n *AudioNormalizer) getProcessConfig() config.ProcessConfig {
	if n.modeTabs.Selected() == n.modeTabs.Items[0] {
		n.advancedMode = false
	} else {
		n.advancedMode = true
	}

	cfg := config.ProcessConfig{
		UseLoudnorm:    n.loudnormCheck.Checked,
		IsSpeech:       n.IsSpeechCheck.Checked,
		OriginIsAAC:    n.checkOriginAAC(),
		WriteTags:      n.writeTags.Checked,
		NoTranscode:    n.noTranscode.Checked,
		DataCompLevel:  int8(math.Round(n.dataCompLevel.Value)),
		BypassProc:     n.bypassProc.Checked,
		DynamicsPreset: n.dynamicsDrop.Selected,
		EqTarget:       n.EqDrop.Selected,
		DynNorm:        n.dynNorm.Checked,
	}

	if n.advancedMode {
		cfg.Format = n.formatSelect.Selected
		cfg.SampleRate = n.sampleRate.Selected
		cfg.BitDepth = n.bitDepth.Selected
		cfg.Bitrate = n.bitrateEntry.Text
		cfg.WriteTags = n.writeTags.Checked
	} else {
		switch n.simpleGroupButtons.Selected {
		case "Small file (AAC 256kbps)":
			cfg.Format = "AAC"
			cfg.Bitrate = "256"
		case "Most compatible (MP3 320kbps)":
			cfg.Format = "MPEG-II L3"
			cfg.Bitrate = "320"
		case "Production (PCM 48kHz/24bit)":
			cfg.Format = "PCM"
			cfg.SampleRate = "48000"
			cfg.BitDepth = "24"
		}
	}

	return cfg
}

func (n *AudioNormalizer) checkOriginAAC() bool {
	originIsAAC := false
	for _, file := range n.files {
		if strings.TrimPrefix(filepath.Ext(file), ".") == "m4a" {
			originIsAAC = true
			break
		}
	}
	return originIsAAC
}

func (n *AudioNormalizer) checkPCM() bool {
	nonTranscoding := false
	for _, file := range n.files {
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(file), "."))
		if ext == "ogg" {
			nonTranscoding = true
			break
		}
	}
	fyne.Do(func() {
		if nonTranscoding {
			n.noTranscode.Disable()
		}
	})
	return nonTranscoding
}

func (n *AudioNormalizer) addFile(path string) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	for _, existing := range n.files {
		if existing == path {
			return
		}
	}

	n.files = append(n.files, path)
	fyne.Do(func() {
		n.fileList.Refresh()
		n.updateProcessButton()
		n.checkPCM()
	})
}

func (n *AudioNormalizer) removeFile(index int) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	n.files = append(n.files[:index], n.files[index+1:]...)

	fyne.Do(func() {
		n.fileList.Refresh()
		n.updateProcessButton()
		n.checkPCM()
	})
}

func (n *AudioNormalizer) updateProcessButton() {
	if len(n.files) > 0 && n.outputDir != "" {
		n.processBtn.Enable()
	} else {
		n.processBtn.Disable()
	}
}

func (n *AudioNormalizer) process() {
	n.processBtn.Disable()
	n.progressBar.Show()
	n.progressBar.SetValue(0)
	n.statusLog.SetText("")

	cfg := n.getProcessConfig()

	workers := runtime.NumCPU() - 1
	if workers < 1 {
		workers = 1
	}

	n.logStatus(fmt.Sprintf("Processing %d files with %d workers...", len(n.files), workers))

	go func() {
		jobs := make(chan string, len(n.files))
		results := make(chan bool, len(n.files))

		var wg sync.WaitGroup

		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for file := range jobs {
					success := n.processFile(file, cfg)
					results <- success
				}
			}()
		}

		for _, file := range n.files {
			jobs <- file
		}
		close(jobs)

		go func() {
			wg.Wait()
			close(results)
		}()

		processed := 0
		successful := 0
		for success := range results {
			processed++
			if success {
				successful++
			}
			progress := float64(processed) / float64(len(n.files))
			fyne.Do(func() {
				n.progressBar.SetValue(progress)
			})
		}

		n.logStatus(fmt.Sprintf("\nComplete: %d/%d files processed successfully", successful, len(n.files)))
		fyne.Do(func() {
			n.processBtn.Enable()
		})
	}()
}

func cleanupTempFiles(tempFiles []string) {
	for _, f := range tempFiles {
		os.Remove(f)
	}
}

func (n *AudioNormalizer) processFile(inputPath string, cfg config.ProcessConfig) bool {
	n.logToFile(n.logFile, fmt.Sprintf("DEBUG config values: EqTarget='%s', DynamicsPreset='%s', BypassProc=%v",
		cfg.EqTarget, cfg.DynamicsPreset, cfg.BypassProc))

	actualCodec := cfg.Format
	var workingPath string = inputPath
	var tempFiles []string
	defer func() { cleanupTempFiles(tempFiles) }()

	if platformCodec := getPlatformCodecMap()[cfg.Format]; platformCodec != "" {
		actualCodec = platformCodec
	} else if codec := config.GetCodec(cfg.Format); codec != "" {
		actualCodec = codec
	}

	n.logToFile(n.logFile, fmt.Sprintf("DEBUG: cfg.Format=%s, actualCodec=%s", cfg.Format, actualCodec))

	baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))

	// Determine output extension
	var ext string
	switch actualCodec {
	case "libopus":
		ext = ".opus"
	case "libfdk_aac":
		ext = ".m4a"
	case "aac":
		ext = ".m4a"
	case "libmp3lame":
		ext = ".mp3"
	case "PCM":
		ext = ".wav"
	case "aac_at":
		ext = ".m4a"
	case "flac":
		ext = ".flac"
	default:
		ext = filepath.Ext(inputPath)
	}

	var outputPath string
	var outputDir string

	if n.batchMode && n.inputDir != "" {
		relPath, err := filepath.Rel(n.inputDir, filepath.Dir(inputPath))
		if err != nil {
			relPath = ""
		}
		outputDir = filepath.Join(n.outputDir, relPath)
		os.MkdirAll(outputDir, 0755)
	} else {
		outputDir = n.outputDir
	}

	originalExt := filepath.Ext(inputPath)

	if cfg.UseLoudnorm {
		outputPath = filepath.Join(outputDir, fmt.Sprintf("%s.normalized%s", baseName, ext))
	} else if cfg.WriteTags && cfg.NoTranscode {
		outputPath = filepath.Join(outputDir, fmt.Sprintf("%s.tagged%s", baseName, originalExt))
	} else if cfg.WriteTags {
		outputPath = filepath.Join(outputDir, fmt.Sprintf("%s.tagged%s", baseName, ext))
	} else if cfg.NoTranscode {
		outputPath = filepath.Join(outputDir, fmt.Sprintf("%s.processed%s", baseName, originalExt))
	} else {
		outputPath = filepath.Join(outputDir, fmt.Sprintf("%s.processed%s", baseName, ext))
	}

	n.logStatus(fmt.Sprintf("→ Processing: %s", filepath.Base(inputPath)))

	// Build base FFmpeg args
	args := []string{"-i", inputPath}

	if actualCodec == "PCM" {
		args = append(args, "-acodec", fmt.Sprintf("pcm_s%sle", cfg.BitDepth))
		args = append(args, "-ar", cfg.SampleRate)
	} else if cfg.NoTranscode {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", actualCodec)
		if cfg.Bitrate != "" && actualCodec != "flac" {
			args = append(args, "-b:a", cfg.Bitrate+"k")
		}
	}

	if cfg.IsSpeech && actualCodec == "libopus" && !n.noTranscode.Checked {
		args = append(args, "-application", "voip")
	} else if !cfg.IsSpeech && actualCodec == "libopus" && !n.noTranscode.Checked {
		args = append(args, "-application", "audio")
	}

	usesDataCompression := actualCodec == "flac" || actualCodec == "libopus"

	if usesDataCompression {
		var level int
		if actualCodec == "libopus" {
			level = 10 - int(cfg.DataCompLevel)
		} else if actualCodec == "flac" {
			level = int(math.Round(float64(cfg.DataCompLevel) * 12.0 / 10.0))
		}
		args = append(args, "-compression_level", fmt.Sprintf("%d", level))
	}

	target := "-23"
	if n.loudnormCustomCheck.Checked && n.normalizeTarget.Text != "" {
		if strings.Contains(n.normalizeTarget.Text, "-") {
			target = n.normalizeTarget.Text
		} else {
			target = "-" + n.normalizeTarget.Text
		}
	}
	if n.normalizeTarget.Text == "" {
		target = "-23"
	}

	targetTp := "-1"
	if n.loudnormCustomCheck.Checked && n.normalizeTargetTp.Text != "" {
		if strings.Contains(n.normalizeTargetTp.Text, "-") {
			targetTp = n.normalizeTargetTp.Text
		} else {
			targetTp = "-" + n.normalizeTargetTp.Text
		}
	}
	if n.normalizeTargetTp.Text == "" {
		targetTp = "-1"
	}

	var eqFilter string
	var multibandFilter string
	var measured map[string]string

	// Stage 1: EQ analysis and application
	if cfg.EqTarget != "" && cfg.EqTarget != "Off" && !cfg.BypassProc {
		eqBandAnalysis := n.analyzeFrequencyResponseBands(workingPath)
		if eqBandAnalysis == nil || len(eqBandAnalysis) == 0 {
			n.logStatus(fmt.Sprintf("✗ Failed to analyze frequency response: %s", filepath.Base(inputPath)))
			return false
		}

		eqFilter = n.buildEqFilter(eqBandAnalysis, cfg.EqTarget)

		if eqFilter != "" {
			eqTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("tnt_eq_%d.wav", time.Now().UnixNano()))
			tempFiles = append(tempFiles, eqTempPath)

			n.logStatus(fmt.Sprintf("→ Applying EQ: %s", filepath.Base(inputPath)))
			cmd := ffmpeg.Command(
				"-i", workingPath,
				"-af", eqFilter,
				"-ar", "192000",
				"-acodec", "pcm_f64le",
				"-y", eqTempPath,
			)

			if err := cmd.Run(); err != nil {
				n.logStatus(fmt.Sprintf("✗ Failed to apply EQ: %s", filepath.Base(inputPath)))
				return false
			}

			workingPath = eqTempPath
			n.logStatus(fmt.Sprintf("✓ EQ applied: %s", filepath.Base(inputPath)))
		}
	}

	// Calculate Dynamics Score if needed
	var dsAnalysis *audio.DynamicsScoreAnalysis
	if !cfg.BypassProc && (cfg.DynamicsPreset != "" && cfg.DynamicsPreset != "Off") {
		dsAnalysis = n.calculateDynamicsScore(inputPath)
		if dsAnalysis == nil {
			n.logStatus(fmt.Sprintf("✗ Failed to calculate Dynamics Score: %s", filepath.Base(inputPath)))
			return false
		}
	}

	// Stage 2: Dynamic normalization (dynaudnorm)
	if cfg.DynNorm && !cfg.BypassProc {
		dynamicsAnalysis := n.analyzeDynamics(workingPath)
		if dynamicsAnalysis == nil {
			n.logStatus(fmt.Sprintf("✗ Failed to analyze for dynaudnorm: %s", filepath.Base(inputPath)))
			return false
		}

		dynParams := audio.CalculateDynaudnormParams(dynamicsAnalysis)
		if dynParams != nil {
			dynaudnormFilter := audio.BuildDynaudnormFilter(dynParams)

			if dynaudnormFilter != "" {
				dynTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("tnt_dyn_%d.wav", time.Now().UnixNano()))
				tempFiles = append(tempFiles, dynTempPath)

				n.logStatus(fmt.Sprintf("→ Applying dynamic normalization: %s", filepath.Base(inputPath)))
				cmd := ffmpeg.Command(
					"-i", workingPath,
					"-af", dynaudnormFilter,
					"-ar", "192000",
					"-acodec", "pcm_f64le",
					"-y", dynTempPath,
				)

				if err := cmd.Run(); err != nil {
					n.logStatus(fmt.Sprintf("✗ Failed to apply dynaudnorm: %s", filepath.Base(inputPath)))
					return false
				}

				workingPath = dynTempPath
				n.logStatus(fmt.Sprintf("✓ Dynamic normalization applied: %s", filepath.Base(inputPath)))
			}
		}
	}

	// Stage 3: Dynamics processing (compression)
	if cfg.DynamicsPreset != "" && cfg.DynamicsPreset != "Off" && !cfg.BypassProc {
		var attenuatedPath string = workingPath

		if cfg.DynamicsPreset == "Broadcast" {
			// Quick peak check for MBC input attenuation
			cmd := ffmpeg.Command("-i", workingPath, "-af", "astats", "-f", "null", "-")
			output, _ := cmd.CombinedOutput()

			peakRe := regexp.MustCompile(`Peak level dB:\s+([-\d.]+)`)
			if match := peakRe.FindStringSubmatch(string(output)); len(match) > 1 {
				peakLevel, _ := strconv.ParseFloat(match[1], 64)

				if peakLevel > -5.0 {
					targetPeak := -6.0
					inputAttenuationDb := targetPeak - peakLevel
					inputVolumeLinear := math.Pow(10, inputAttenuationDb/20)

					attenPath := filepath.Join(os.TempDir(), fmt.Sprintf("tnt_atten_%d.wav", time.Now().UnixNano()))
					tempFiles = append(tempFiles, attenPath)

					cmd := ffmpeg.Command(
						"-i", workingPath,
						"-af", fmt.Sprintf("volume=%.6f", inputVolumeLinear),
						"-ar", "192000",
						"-acodec", "pcm_f64le",
						"-y", attenPath,
					)

					if err := cmd.Run(); err == nil {
						attenuatedPath = attenPath
					}
				}
			}

			// MBC: analyze frequency bands
			bandAnalysis := n.analyzeFrequencyBands(attenuatedPath)
			if bandAnalysis != nil && len(bandAnalysis) > 0 {
				multibandFilter = n.buildMultibandCompression(bandAnalysis, dsAnalysis, cfg.DynamicsPreset)
			}
		} else {
			// SBC: single-band compression
			dynamicsAnalysis := n.analyzeDynamics(workingPath)
			if dynamicsAnalysis != nil {
				singleBandFilter := n.calculateAdaptiveCompression(dynamicsAnalysis, dsAnalysis, cfg.DynamicsPreset)
				if singleBandFilter != "" {
					compTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("tnt_comp_%d.wav", time.Now().UnixNano()))
					tempFiles = append(tempFiles, compTempPath)

					cmd := ffmpeg.Command(
						"-i", workingPath,
						"-af", singleBandFilter,
						"-ar", "192000",
						"-acodec", "pcm_f64le",
						"-y", compTempPath,
					)

					if err := cmd.Run(); err == nil {
						workingPath = compTempPath
					}
				}
			}
		}

		// Apply multiband if built
		if multibandFilter != "" {
			mbcTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("tnt_mbc_%d.wav", time.Now().UnixNano()))
			tempFiles = append(tempFiles, mbcTempPath)

			n.logStatus(fmt.Sprintf("→ Applying multiband compression: %s", filepath.Base(inputPath)))
			cmd := ffmpeg.Command(
				"-i", workingPath,
				"-af", multibandFilter,
				"-ar", "192000",
				"-acodec", "pcm_f64le",
				"-y", mbcTempPath,
			)

			if err := cmd.Run(); err != nil {
				n.logStatus(fmt.Sprintf("✗ Failed to apply MBC: %s", filepath.Base(inputPath)))
				return false
			}

			workingPath = mbcTempPath
			n.logStatus(fmt.Sprintf("✓ Multiband compression applied: %s", filepath.Base(inputPath)))
		}
	}

	// Stage 4: Measure loudness for normalization
	if cfg.UseLoudnorm {
		measured = n.measureLoudness(workingPath)
		if measured == nil {
			n.logStatus(fmt.Sprintf("✗ Failed to measure: %s", filepath.Base(inputPath)))
			return false
		}
	}

	if cfg.WriteTags {
		measured = n.measureLoudnessEbuR128(workingPath)
		if measured == nil {
			n.logStatus(fmt.Sprintf("✗ Failed to measure: %s", filepath.Base(inputPath)))
			return false
		}
	}

	// Build final filter chain
	var loudnormFilterChain string
	if cfg.UseLoudnorm && measured != nil {
		if cfg.IsSpeech {
			loudnormFilterChain = fmt.Sprintf(
				"speechnorm=e=12.5:r=0.0001:l=1,loudnorm=I=%s:TP=%s:LRA=5.0:measured_I=%s:measured_TP=%s:measured_LRA=%s:measured_thresh=%s:linear=true",
				target, targetTp,
				measured["input_i"], measured["input_tp"], measured["input_lra"], measured["input_thresh"],
			)
		} else {
			loudnormFilterChain = fmt.Sprintf(
				"loudnorm=I=%s:TP=%s:LRA=5.0:measured_I=%s:measured_TP=%s:measured_LRA=%s:measured_thresh=%s:offset=%s:linear=true",
				target, targetTp,
				measured["input_i"], measured["input_tp"], measured["input_lra"], measured["input_thresh"], measured["target_offset"],
			)
		}
	}

	var finalFilterChain string
	var filterStages []string

	if loudnormFilterChain != "" {
		filterStages = append(filterStages, loudnormFilterChain)
	}

	if len(filterStages) > 0 {
		finalFilterChain = strings.Join(filterStages, ",")
	}

	args[1] = workingPath

	// Add dithering for 16-bit PCM output
	if actualCodec == "PCM" && cfg.BitDepth == "16" {
		if finalFilterChain != "" {
			finalFilterChain += ",dither=triangular_hp"
		} else {
			finalFilterChain = "dither=triangular_hp"
		}
	}

	if finalFilterChain != "" {
		args = append(args, "-af", finalFilterChain)
	}

	// Add metadata tags if requested
	if cfg.WriteTags && measured != nil {
		args = append(args, "-metadata", fmt.Sprintf("REPLAYGAIN_TRACK_GAIN=%s dB", measured["input_i"]))
		args = append(args, "-metadata", fmt.Sprintf("REPLAYGAIN_TRACK_PEAK=%s", measured["input_tp"]))
	}

	args = append(args, "-y", outputPath)

	n.logStatus(fmt.Sprintf("→ Encoding: %s", filepath.Base(inputPath)))

	cmd := ffmpeg.Command(args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		n.logStatus(fmt.Sprintf("✗ Failed: %s", filepath.Base(inputPath)))
		n.logToFile(n.logFile, fmt.Sprintf("FFmpeg error: %v\nOutput: %s", err, string(output)))
		return false
	}

	n.logStatus(fmt.Sprintf("✓ Complete: %s → %s", filepath.Base(inputPath), filepath.Base(outputPath)))
	return true
}

// Analysis functions using audio package parsing

func (n *AudioNormalizer) analyzeDynamics(inputPath string) *audio.DynamicsAnalysis {
	cmd := ffmpeg.Command(
		"-i", inputPath,
		"-af", "astats=metadata=1:length=0.05",
		"-f", "null",
		"-",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		n.logToFile(n.logFile, fmt.Sprintf("astats failed: %v", err))
		return nil
	}

	return audio.ParseAstatsOutput(string(output))
}

func (n *AudioNormalizer) calculateDynamicsScore(inputPath string) *audio.DynamicsScoreAnalysis {
	n.logStatus(fmt.Sprintf("→ Calculating Dynamics Score: %s", filepath.Base(inputPath)))

	cmd := ffmpeg.Command(
		"-i", inputPath,
		"-af", "astats",
		"-f", "null",
		"-",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		n.logToFile(n.logFile, fmt.Sprintf("DS calculation failed: %v", err))
		return nil
	}

	result := audio.ParseDynamicsScore(string(output))

	n.logToFile(n.logFile, fmt.Sprintf("DS Analysis - RMS Peak: %.2f dB, RMS Level: %.2f dB, Crest: %.2f",
		result.RMSPeak, result.RMSLevel, result.CrestFactor))
	n.logToFile(n.logFile, fmt.Sprintf("Dynamics Score: %.2f", result.DynamicsScore))

	return result
}

func (n *AudioNormalizer) analyzeFrequencyBands(inputPath string) map[string]*audio.FrequencyBandAnalysis {
	bands := audio.FrequencyBandFilters()
	results := make(map[string]*audio.FrequencyBandAnalysis)

	for bandName, filter := range bands {
		cmd := ffmpeg.Command(
			"-i", inputPath,
			"-af", fmt.Sprintf("%s,astats=metadata=1:length=0.05", filter),
			"-f", "null",
			"-",
		)

		output, err := cmd.CombinedOutput()
		if err != nil {
			continue
		}

		analysis := audio.ParseFrequencyBandOutput(string(output), bandName)
		if analysis != nil {
			results[bandName] = analysis
		}
	}

	return results
}

func (n *AudioNormalizer) measureLoudness(inputPath string) map[string]string {
	n.logStatus(fmt.Sprintf("→ Measuring: %s", filepath.Base(inputPath)))

	target := "-23"
	if (n.loudnormCustomCheck.Checked || n.writeTags.Checked) && n.normalizeTarget.Text != "" {
		if strings.Contains(n.normalizeTarget.Text, "-") {
			target = n.normalizeTarget.Text
		} else {
			target = "-" + n.normalizeTarget.Text
		}
	}

	targetTp := "-1"
	if (n.loudnormCustomCheck.Checked || n.writeTags.Checked) && n.normalizeTargetTp.Text != "" {
		if strings.Contains(n.normalizeTargetTp.Text, "-") {
			targetTp = n.normalizeTargetTp.Text
		} else {
			targetTp = "-" + n.normalizeTargetTp.Text
		}
	}

	cmd := ffmpeg.Command(
		"-i", inputPath,
		"-af", fmt.Sprintf("loudnorm=linear=false:I=%s:TP=%s:LRA=5:print_format=json", target, targetTp),
		"-f", "null",
		"-",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}

	return n.parseLoudnormJSON(string(output))
}

func (n *AudioNormalizer) measureLoudnessEbuR128(inputPath string) map[string]string {
	cmd := ffmpeg.Command(
		"-i", inputPath,
		"-af", "ebur128=framelog=quiet:peak=true",
		"-f", "null",
		"-",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}

	return n.parseEBUR128Output(string(output))
}

func (n *AudioNormalizer) parseLoudnormJSON(output string) map[string]string {
	re := regexp.MustCompile(`(?s)\{[^\}]*"input_i"[^\}]*\}`)
	jsonMatch := re.FindString(output)

	if jsonMatch == "" {
		return nil
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonMatch), &data); err != nil {
		return nil
	}

	result := make(map[string]string)
	for key, value := range data {
		if str, ok := value.(string); ok {
			result[key] = str
		}
	}

	return result
}

func (n *AudioNormalizer) parseEBUR128Output(output string) map[string]string {
	result := make(map[string]string)

	iRe := regexp.MustCompile(`I:\s+([-\d.]+)\s+LUFS`)
	if match := iRe.FindStringSubmatch(output); len(match) > 1 {
		result["input_i"] = match[1]
	}

	lraRe := regexp.MustCompile(`LRA:\s+([-\d.]+)\s+LU`)
	if match := lraRe.FindStringSubmatch(output); len(match) > 1 {
		result["input_lra"] = match[1]
	}

	threshRe := regexp.MustCompile(`Threshold:\s+([-\d.]+)\s+LUFS`)
	if match := threshRe.FindStringSubmatch(output); len(match) > 1 {
		result["input_thresh"] = match[1]
	}

	pkRe := regexp.MustCompile(`Peak:\s+([-\d.]+)\s+dBFS`)
	if match := pkRe.FindStringSubmatch(output); len(match) > 1 {
		result["input_tp"] = match[1]
	}

	return result
}

// Compression functions using audio package calculations

func (n *AudioNormalizer) calculateAdaptiveCompression(analysis *audio.DynamicsAnalysis, dsAnalysis *audio.DynamicsScoreAnalysis, preset string) string {
	if analysis == nil || preset == "Off" {
		return ""
	}

	var threshold, ratio, attack, release float64

	switch preset {
	case "Light":
		threshold = analysis.RMSLevel + 6.0
		ratio = audio.GetBaseRatioFromCrest(analysis.CrestFactor)
		attack = 100
		release = 250
	case "Moderate":
		threshold = analysis.RMSLevel + 5.0
		ratio = audio.GetBaseRatioFromCrest(analysis.CrestFactor)
		attack = 40
		release = 150
	case "Broadcast":
		threshold = analysis.RMSLevel + 4.0
		ratio = audio.GetBaseRatioFromCrest(analysis.CrestFactor)
		attack = 10
		release = 30
	}

	if dsAnalysis != nil {
		mods := audio.GetCompressionModifiers(dsAnalysis.DynamicsScore)
		attack *= mods.AttackMultiplier
		release *= mods.ReleaseMultiplier
		ratio *= mods.RatioMultiplier

		n.logToFile(n.logFile, fmt.Sprintf("DS Modifiers - Attack: %.1fx, Release: %.1fx, Ratio: %.1fx",
			mods.AttackMultiplier, mods.ReleaseMultiplier, mods.RatioMultiplier))
	}

	thresholdLin := audio.DbToLinear(threshold)
	makeupGain := audio.CalculateMakeupGain(analysis, threshold, ratio)
	knee := audio.GetKneeFromRatio(ratio)

	thresholdLin, ratio, attack, release, makeupGain = audio.ClampCompressorParams(thresholdLin, ratio, attack, release, makeupGain)

	limiterLin := audio.DbToLinear(-1.0)

	return fmt.Sprintf("acompressor=threshold=%.6f:ratio=%.1f:attack=%.1f:release=%.1f:makeup=1.0:knee=%.1f,alimiter=limit=%.6f:attack=5:release=50:level=false,volume=%.3f",
		thresholdLin, ratio, attack, release, knee, limiterLin, makeupGain)
}

func (n *AudioNormalizer) buildMultibandCompression(bandAnalysis map[string]*audio.FrequencyBandAnalysis, dsAnalysis *audio.DynamicsScoreAnalysis, preset string) string {
	if len(bandAnalysis) == 0 {
		return ""
	}

	var mods audio.CompressionModifiers
	if dsAnalysis != nil {
		mods = audio.GetCompressionModifiers(dsAnalysis.DynamicsScore)
	} else {
		mods = audio.CompressionModifiers{AttackMultiplier: 1.0, ReleaseMultiplier: 1.0, RatioMultiplier: 1.0}
	}

	subFilter := n.buildBandAcompressor(bandAnalysis["sub"], 20.0, 100.0, 4.0, -20.0, mods)
	bassFilter := n.buildBandAcompressor(bandAnalysis["bass"], 15.0, 80.0, 3.5, -18.0, mods)
	lowMidFilter := n.buildBandAcompressor(bandAnalysis["low_mid"], 10.0, 60.0, 3.0, -16.0, mods)
	midFilter := n.buildBandAcompressor(bandAnalysis["mid"], 8.0, 50.0, 2.5, -14.0, mods)
	highFilter := n.buildBandAcompressor(bandAnalysis["high"], 5.0, 40.0, 2.0, -12.0, mods)

	filterChain := "aresample=192000,"
	filterChain += fmt.Sprintf(
		"acrossover=split=80 250 1000 4000:order=4th:precision=double[SUB][LOW][LMID][HMID][HI];"+
			"[SUB]%s[sub_out];"+
			"[LOW]%s[low_out];"+
			"[LMID]%s[lmid_out];"+
			"[HMID]%s[hmid_out];"+
			"[HI]%s[hi_out];"+
			"[sub_out][low_out][lmid_out][hmid_out][hi_out]amix=inputs=5:normalize=0,"+
			"alimiter=limit=0.9886:level=false",
		subFilter, bassFilter, lowMidFilter, midFilter, highFilter)

	return filterChain
}

func (n *AudioNormalizer) buildBandAcompressor(band *audio.FrequencyBandAnalysis, attackMs float64, releaseMs float64, ratio float64, fallbackThresholdDb float64, mods audio.CompressionModifiers) string {
	if band == nil {
		thresholdLin := audio.DbToLinear(fallbackThresholdDb)
		makeup := audio.DbToLinear(3.0)
		limiterLin := audio.DbToLinear(-1.0)
		return fmt.Sprintf("acompressor=threshold=%.6f:ratio=%.1f:attack=%.1f:release=%.1f:makeup=1.0,alimiter=limit=%.6f:attack=5:release=50,volume=%.3f",
			thresholdLin, ratio, attackMs, releaseMs, limiterLin, makeup)
	}

	var adaptiveThresholdDb float64
	if mods.RatioMultiplier < 0.3 {
		adaptiveThresholdDb = band.PeakLevel - 1.0
	} else {
		thresholdOffset := 6.0
		if mods.RatioMultiplier > 3.0 {
			thresholdOffset = 3.0
		}
		adaptiveThresholdDb = band.RMSLevel + thresholdOffset
	}

	thresholdLin := audio.DbToLinear(adaptiveThresholdDb)

	var makeupGainDb float64
	if mods.RatioMultiplier < 0.3 {
		makeupGainDb = 0.0
	} else {
		expectedGRDb := (band.RMSLevel - adaptiveThresholdDb) / ratio
		makeupGainDb = -expectedGRDb * 0.8
		if makeupGainDb < 0 {
			makeupGainDb = 0
		}
	}
	makeupLin := audio.DbToLinear(makeupGainDb)

	var limiterCeilingDb float64
	if mods.RatioMultiplier < 0.3 {
		limiterCeilingDb = band.PeakLevel - 0.1
		if limiterCeilingDb > 0.0 {
			limiterCeilingDb = 0.0
		}
	} else {
		limiterCeilingDb = band.PeakLevel - 0.8
	}
	if limiterCeilingDb < -24.0 {
		limiterCeilingDb = -24.0
	}

	limiterLin := audio.DbToLinear(limiterCeilingDb)
	if limiterLin > 1.0 {
		limiterLin = 1.0
	}

	attackMs *= mods.AttackMultiplier
	releaseMs *= mods.ReleaseMultiplier
	ratio *= mods.RatioMultiplier

	limiterAttack := 25.0 * mods.AttackMultiplier
	limiterRelease := 150.0 * mods.ReleaseMultiplier

	knee := audio.GetKneeFromRatio(ratio)

	thresholdLin, ratio, attackMs, releaseMs, makeupLin = audio.ClampCompressorParams(thresholdLin, ratio, attackMs, releaseMs, makeupLin)

	if limiterAttack > 80.0 {
		limiterAttack = 80.0
	}
	if limiterRelease > 8000.0 {
		limiterRelease = 8000.0
	}

	return fmt.Sprintf("acompressor=threshold=%.6f:ratio=%.1f:attack=%.1f:release=%.1f:makeup=1.0:knee=%.1f,alimiter=limit=%.6f:attack=%.0f:release=%.0f:level=false,volume=%.3f",
		thresholdLin, ratio, attackMs, releaseMs, knee, limiterLin, limiterAttack, limiterRelease, makeupLin)
}

// Watch mode

func (n *AudioNormalizer) startWatcher() {
	n.watcherMutex.Lock()
	defer n.watcherMutex.Unlock()

	if n.watching {
		return
	}

	n.watching = true
	n.watcherStop = make(chan bool)
	n.jobQueue = make(chan string, 100)

	go n.watchDirectory()
	go n.processWatchQueue()

	fyne.Do(func() {
		n.watcherWarnLabel.SetText("Watch mode active")
	})
}

func (n *AudioNormalizer) stopWatcher() {
	n.watcherMutex.Lock()
	defer n.watcherMutex.Unlock()

	if !n.watching {
		return
	}

	close(n.watcherStop)
	n.watching = false

	fyne.Do(func() {
		n.watcherWarnLabel.SetText("")
	})
}

func (n *AudioNormalizer) watchDirectory() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		n.logStatus("Failed to create watcher: " + err.Error())
		return
	}
	defer watcher.Close()

	err = watcher.Add(n.inputDir)
	if err != nil {
		n.logStatus("Failed to watch directory: " + err.Error())
		return
	}

	for {
		select {
		case event := <-watcher.Events:
			if event.Op&fsnotify.Create == fsnotify.Create && isAudioFile(event.Name) {
				select {
				case n.jobQueue <- event.Name:
				case <-n.watcherStop:
					return
				}
			}
		case <-n.watcherStop:
			return
		case err := <-watcher.Errors:
			n.logStatus("Watcher error: " + err.Error())
		}
	}
}

func (n *AudioNormalizer) processWatchQueue() {
	for {
		select {
		case file := <-n.jobQueue:
			n.processFile(file, n.getProcessConfig())
		case <-n.watcherStop:
			return
		}
	}
}

// File selection and folder handling

func (n *AudioNormalizer) selectFiles() {
	dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		path := reader.URI().Path()
		reader.Close()

		if isAudioFile(path) {
			n.addFile(path)
		}
	}, n.window)
}

func (n *AudioNormalizer) selectFolder() {
	dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil || uri == nil {
			return
		}

		n.inputDir = uri.Path()
		n.batchMode = true

		filepath.WalkDir(uri.Path(), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() && isAudioFile(path) {
				n.addFile(path)
			}
			return nil
		})
	}, n.window)
}

func (n *AudioNormalizer) selectOutputFolder() {
	dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil || uri == nil {
			return
		}

		n.outputDir = uri.Path()
		fyne.Do(func() {
			n.outputLabel.SetText(uri.Path())
			n.updateProcessButton()
		})
	}, n.window)
}

// Logging

func (n *AudioNormalizer) initLogFile() *os.File {
	var logDir string

	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		logDir = filepath.Join(home, "Library", "Application Support", "TNT")
	case "windows":
		logDir = filepath.Join(os.Getenv("APPDATA"), "TNT")
	default:
		home, _ := os.UserHomeDir()
		logDir = filepath.Join(home, ".tnt")
	}

	os.MkdirAll(logDir, 0755)

	logPath := filepath.Join(logDir, fmt.Sprintf("tnt_%s.log", time.Now().Format("2006-01-02")))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil
	}

	return logFile
}

func main() {
	a := app.NewWithID("com.collinsgroup.tnt")
	a.Settings().SetTheme(&appleTheme{})

	w := a.NewWindow("TNT - Transcode, Normalize, Tag")
	w.Resize(fyne.NewSize(650, 600))

	norm := &AudioNormalizer{
		window: w,
		files:  make([]string, 0),
	}

	norm.setupUI(a)
	norm.loadPreferences()

	norm.logFile = norm.initLogFile()
	if norm.logFile != nil {
		defer norm.logFile.Close()
	}

	go checkForUpdates(currentVersion, w, norm.logFile)

	w.ShowAndRun()
}

func getLogoForTheme(a fyne.App) fyne.Resource {
	if a.Settings().ThemeVariant() == theme.VariantDark {
		return resourceTntAppLogoForDarkPng
	}
	return resourceTntAppLogoForLightPng
}
