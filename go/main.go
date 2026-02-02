package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
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
	"github.com/fremen-fi/tnt/go/platform"
)

const (
	currentVersion = "1.3.0"
	versionCheckURL = "https://software.collinsgroup.fi/tnt-version.json"
	macARMDownloadURL = "https://software.collinsgroup.fi/TNT.dmg"
	macIntelDownloadURL = "https://software.collinsgroup.fi/TNT-Intel.dmg"
	linuxDownloadURL = "https://software.collinsgroup.fi/tnt.deb"
	windowsDownloadURL = "https://software.collinsgroup.fi/TNT.exe"
)

type VersionInfo struct {
	Version      string              `json:"version"`
	OS           []string            `json:"os"`
	DownloadURL  []map[string]string `json:"download_url"`
	ReleaseNotes string              `json:"release_notes"`
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

	modeTabs *container.AppTabs
	modeWarning *widget.Label

	// Mode toggle
	advancedMode bool
	modeToggle   *widget.Check

	// Simple mode
	simpleGroupButtons *widget.RadioGroup
	simpleGroup *fyne.Container

	// Advanced mode
	formatSelect   *widget.Select
	sampleRate     *widget.Select
	bitDepth       *widget.Select
	bitrateEntry   *widget.Entry
	normalizeTarget *widget.Entry
	normalizeTargetTp *widget.Entry
	advancedContainer *fyne.Container

	// Common
	loudnormCheck *widget.Check
	loudnormCustomCheck *widget.Check
	loudnormLabel *widget.Label
	writeTagsLabel *widget.Label
	normalizeTargetLabel *widget.Label
	normalizeTargetLabelTp *widget.Label
	normalizationStandard string
	IsSpeechCheck *widget.Check
	writeTags *widget.Check
	noTranscode *widget.Check
	dataCompLevel *widget.Slider

	// dynamics
	dynamicsLabel *widget.Label
	dynamicsDrop *widget.Select
	EqLabel *widget.Label
	EqDrop *widget.Select
	//dynNormLabel *widget.Label
	dynNorm *widget.Check
	dynNormLabel *widget.Label
	bypassProc *widget.Check

	multibandFilter string

	logFile *os.File

	// watchmode
	watchMode *widget.Check
	watching bool
	watcherStop chan bool
	jobQueue chan string
	inputDir string
	watcherWarnLabel *widget.Label

	watcherMutex sync.Mutex

	// phase check items
	checkPhaseBtn *widget.Check

	// batch processing
	batchMode bool

	menuWindow fyne.Window
	menuMutex  sync.Mutex

	mutex sync.Mutex
}

type ProcessConfig struct {
	Format      string
	SampleRate  string
	BitDepth    string
	Bitrate     string
	UseLoudnorm bool
	CustomLoudnorm bool
	IsSpeech bool
	writeTags bool
	noTranscode bool
	originIsAAC bool
	dataCompLevel int8
	DynamicsPreset string
	bypassProc bool
	EqTarget string
	DynNorm bool
	PhaseCheck bool
}

type DynamicsAnalysis struct {
	PeakLevel     float64
	RMSPeak       float64
	RMSTrough     float64
	CrestFactor   float64
	DynamicRange  float64
	RMSLevel      float64
	NoiseFloor float64
}

type FrequencyBandAnalysis struct {
	BandName     string
	PeakLevel    float64
	RMSLevel     float64
	CrestFactor  float64
	DynamicRange float64
}

func getPlatformKey() string {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "darwin"
		}
		return "darwin-senior"
	case "windows":
		return "orangutan"
	case "linux":
		return "penguin"
	default:
		return runtime.GOOS
	}
}

func checkForUpdates(currentVersion string, window fyne.Window, logFile *os.File) {
	logToFile(logFile, "Starting update check...")
	time.Sleep(500 * time.Millisecond)

	logToFile(logFile, "Fetching version info from server...")
	resp, err := http.Get(versionCheckURL)
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
						downloadAndInstallUpdate(versionInfo, window)
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
	// Parse versions into major.minor.patch
	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")

	// Ensure we have 3 parts for both
	for len(parts1) < 3 {
		parts1 = append(parts1, "0")
	}
	for len(parts2) < 3 {
		parts2 = append(parts2, "0")
	}

//	for i := 0; i < limit; i++ {}
//
// by a range loop with an integer operand:
//
//	for i := range limit {}

// below modernized

	// Compare each part numerically
	for i := range 3 {
		n1, _ := strconv.Atoi(parts1[i])
		n2, _ := strconv.Atoi(parts2[i])

		if n1 > n2 {
			return 1
		} else if n1 < n2 {
			return -1
		}
	}

	return 0
}

func downloadAndInstallUpdate(versionInfo VersionInfo, window fyne.Window) {
logFile, _ := os.OpenFile(filepath.Join(os.TempDir(), "tnt_update.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
defer logFile.Close()

logToFile(logFile, "Starting update download...")

// Get platform-specific download URL
platformKey := getPlatformKey()
var downloadURL string

// Search download_url array for matching platform
for _, urlMap := range versionInfo.DownloadURL {
	if url, ok := urlMap[platformKey]; ok {
		downloadURL = url
		break
	}
}

if downloadURL == "" {
	logToFile(logFile, fmt.Sprintf("No download URL found for platform: %s", platformKey))
	dialog.ShowError(fmt.Errorf("Update not available for your platform"), window)
	return
}

logToFile(logFile, fmt.Sprintf("Platform: %s, Download URL: %s", platformKey, downloadURL))

// Determine file extension
var fileName string
switch platformKey {
case "darwin", "darwin-senior":
	fileName = "TNT.dmg"
case "orangutan":
	fileName = "TNT-Setup.exe"
case "penguin":
	fileName = "tnt.deb"
}

	logToFile(logFile, fmt.Sprintf("Download URL: %s", downloadURL))

	// Download file
	var progressDialog dialog.Dialog
	fyne.Do(func() {
		progressDialog = dialog.NewCustom("Downloading Update", "Cancel",
			widget.NewProgressBarInfinite(), window)
		progressDialog.Show()
	})

	tempPath := filepath.Join(os.TempDir(), fileName)

	go func() {
		resp, err := http.Get(downloadURL)
		if err != nil {
			logToFile(logFile, fmt.Sprintf("Download failed: %v", err))
			fyne.Do(func() {
				progressDialog.Hide()
			})
			dialog.ShowError(err, window)
			return
		}
		defer resp.Body.Close()

		out, err := os.Create(tempPath)
		if err != nil {
			logToFile(logFile, fmt.Sprintf("File create failed: %v", err))
			progressDialog.Hide()
			dialog.ShowError(err, window)
			return
		}
		defer out.Close()

		_, err = io.Copy(out, resp.Body)
		if err != nil {
			logToFile(logFile, fmt.Sprintf("File write failed: %v", err))
			fyne.Do(func() {
				progressDialog.Hide()
			})
			dialog.ShowError(err, window)
			return
		}

		fyne.Do(func() {
			progressDialog.Hide()
		})
		logToFile(logFile, fmt.Sprintf("Downloaded to: %s", tempPath))

		// Show install prompt
		fyne.Do(func() {
			dialog.ShowConfirm(
				"Update Ready",
				fmt.Sprintf("Version %s has been downloaded.\n\nInstall now?", versionInfo.Version),
				func(install bool) {
					if install {
						var cmd *exec.Cmd
						switch runtime.GOOS {
						case "darwin":
							cmd = exec.Command("open", tempPath)
						case "windows":
							cmd = exec.Command("cmd", "/c", "start", "", tempPath)
						case "linux":
							cmd = exec.Command("xdg-open", tempPath)
						}
						cmd.Start()
						logToFile(logFile, "Installer opened")
					}
				},
				window,
			)
		})
	}()
}

func extractFFmpeg() string {
	// Extract to temp location
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

var ffmpegPath string

func init() {
	ffmpegPath = extractFFmpeg()
}

func (n *AudioNormalizer) initLogFile() *os.File {
	configDir, _ := os.UserConfigDir()
	logDir := filepath.Join(configDir, "TNT")
	os.MkdirAll(logDir, 0755)

	logPath := filepath.Join(logDir, "tnt.log")

	if data, err := os.ReadFile(logPath); err == nil {
		lines := strings.Count(string(data), "\n")
		if lines > 1000 { // Keep last 1000 lines
			allLines := strings.Split(string(data), "\n")
			keepLines := allLines[len(allLines)-1000:]
			os.WriteFile(logPath, []byte(strings.Join(keepLines, "\n")), 0644)
		}
	}

	logfile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil
	}

	return logfile
}

func (n *AudioNormalizer) logToFile(logFile *os.File, message string) {
	if logFile != nil {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		logFile.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, message))
	}
}

func (n *AudioNormalizer) sendLogReport() {
	configDir, _ := os.UserConfigDir()
	logPath := filepath.Join(configDir, "TNT", "tnt.log")

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		dialog.ShowInformation("No Log File", "No log file found. Try processing some files first.", n.window)
		return
	}

	subject := "TNT Error Report"
	body := fmt.Sprintf("OS: %s\nVersion: %s\n\nPlease describe what happened:\n\n", runtime.GOOS, currentVersion)

	var cmd *exec.Cmd
	var copyLocation string

	switch runtime.GOOS {
	case "darwin":
		// macOS: Use osascript to create email with attachment
		script := fmt.Sprintf(`tell application "Mail"
			set theMessage to make new outgoing message with properties {subject:"%s", content:"%s", visible:true}
			tell theMessage
				make new to recipient with properties {address:"appsupport@collinsgroup.fi"}
				make new attachment with properties {file name:POSIX file "%s"}
			end tell
			activate
		end tell`, subject, body, logPath)
		cmd = exec.Command("osascript", "-e", script)
	case "linux":
		cmd = exec.Command("xdg-email",
			"--subject", subject,
			"--body", body,
			"--attach", logPath,
			"appsupport@collinsgroup.fi")
	case "windows":
		// Copy log to Desktop with clear name
		homeDir, _ := os.UserHomeDir()
		copyLocation = filepath.Join(homeDir, "Desktop", "TNT-error-log.txt")
		input, _ := os.ReadFile(logPath)
		os.WriteFile(copyLocation, input, 0644)

		// Open default email client with mailto
		mailtoURL := fmt.Sprintf("mailto:appsupport@collinsgroup.fi?subject=%s&body=%s",
			strings.ReplaceAll(subject, " ", "%20"),
			strings.ReplaceAll(body, "\n", "%0D%0A"))
		exec.Command("cmd", "/c", "start", mailtoURL)

	}

	if cmd != nil {
		if runtime.GOOS == "windows" {
			if err := cmd.Start(); err != nil {
				dialog.ShowError(fmt.Errorf("Failed to launch email client: %w", err), n.window)
			}
		} else if err := cmd.Run(); err != nil {
			// Use Run() for other OSes (darwin, linux)
			dialog.ShowError(fmt.Errorf("Failed to open email client. Log file location:\n%s", logPath), n.window)
		}
	}

	if runtime.GOOS == "windows" && copyLocation != "" {
		dialog.ShowInformation("Attach Log File",
			fmt.Sprintf("Log file copied to your Desktop:\n%s\n\nPlease attach it to the email. If no native email client was found, none was opened. In this case, send the email manually.", filepath.Base(copyLocation)),
			n.window)
	}
}

func (n *AudioNormalizer) analyzeDynamics(inputPath string) *DynamicsAnalysis {
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

	// LOG THE RAW OUTPUT
	//n.logToFile(n.logFile, "=== RAW ASTATS OUTPUT START ===")
	//n.logToFile(n.logFile, string(output))
	//n.logToFile(n.logFile, "=== RAW ASTATS OUTPUT END ===")

	return n.parseAstatsOutput(string(output))
}

func (n *AudioNormalizer) analyzeFrequencyBands(inputPath string) map[string]*FrequencyBandAnalysis {
	bands := map[string]string{
		"sub":     "lowpass=f=80",
		"bass":    "highpass=f=80,lowpass=f=250",
		"low_mid": "highpass=f=250,lowpass=f=1000",
		"mid":     "highpass=f=1000,lowpass=f=4000",
		"high":    "highpass=f=4000",
	}

	results := make(map[string]*FrequencyBandAnalysis)

	n.logToFile(n.logFile, fmt.Sprintf("=== FREQUENCY BAND ANALYSIS START: %s ===", filepath.Base(inputPath)))

	for bandName, filter := range bands {
		cmd := exec.Command(
			ffmpegPath,
			"-i", inputPath,
			"-af", fmt.Sprintf("%s,astats", filter),
			"-f", "null",
			"-",
		)


		output, err := cmd.CombinedOutput()
		if err != nil {
			n.logToFile(n.logFile, fmt.Sprintf("Band %s analysis failed: %v", bandName, err))
			continue
		}

		// Log raw output for this band
		//n.logToFile(n.logFile, fmt.Sprintf("--- RAW OUTPUT FOR BAND: %s ---", bandName))
		//n.logToFile(n.logFile, string(output))
		//n.logToFile(n.logFile, "--- END RAW OUTPUT ---")

		// Parse the output
		analysis := n.parseFrequencyBandOutput(string(output), bandName)
		if analysis != nil {
			results[bandName] = analysis

			// Log parsed results
			n.logToFile(n.logFile, fmt.Sprintf("Band %s Results:", bandName))
			n.logToFile(n.logFile, fmt.Sprintf("  Peak: %.2f dBFS", analysis.PeakLevel))
			n.logToFile(n.logFile, fmt.Sprintf("  RMS: %.2f dBFS", analysis.RMSLevel))
			n.logToFile(n.logFile, fmt.Sprintf("  Crest: %.2f", analysis.CrestFactor))
			n.logToFile(n.logFile, fmt.Sprintf("  Range: %.2f dB", analysis.DynamicRange))
		}
	}

	n.logToFile(n.logFile, "=== FREQUENCY BAND ANALYSIS END ===")

	return results
}

func (n *AudioNormalizer) parseFrequencyBandOutput(output string, bandName string) *FrequencyBandAnalysis {
	result := &FrequencyBandAnalysis{BandName: bandName}

	// Find Overall section
	overallStart := strings.Index(output, "Overall")
	if overallStart == -1 {
		return nil
	}
	overallSection := output[overallStart:]

	// Parse: Peak level dB: -65.832755
	peakRe := regexp.MustCompile(`Peak level dB:\s+([-\d.]+)`)
	if match := peakRe.FindStringSubmatch(overallSection); len(match) > 1 {
		result.PeakLevel, _ = strconv.ParseFloat(match[1], 64)
	}

	// Parse: RMS level dB: -76.472639
	rmsRe := regexp.MustCompile(`RMS level dB:\s+([-\d.]+)`)
	if match := rmsRe.FindStringSubmatch(overallSection); len(match) > 1 {
		result.RMSLevel, _ = strconv.ParseFloat(match[1], 64)
	}

	// Parse: Crest factor: 2.982689 (from channel section)
	crestRe := regexp.MustCompile(`Crest factor:\s+([-\d.]+)`)
	if match := crestRe.FindStringSubmatch(output); len(match) > 1 {
		result.CrestFactor, _ = strconv.ParseFloat(match[1], 64)
	}

	// Parse: Dynamic range: 51.779619 (from channel section)
	dynRe := regexp.MustCompile(`Dynamic range:\s+([-\d.]+)`)
	if match := dynRe.FindStringSubmatch(output); len(match) > 1 {
		result.DynamicRange, _ = strconv.ParseFloat(match[1], 64)
	}

	return result
}

func (n *AudioNormalizer) buildMultibandCompression(bandAnalysis map[string]*FrequencyBandAnalysis, dsAnalysis *audio.DynamicsScoreAnalysis, preset string) string {
		if len(bandAnalysis) == 0 || preset == "Off" {
		return ""
	}

	n.logToFile(n.logFile, fmt.Sprintf("=== BUILDING MULTIBAND COMPRESSION (%s) ===", preset))

	var mods audio.CompressionModifiers
	if dsAnalysis != nil {
		mods = audio.GetCompressionModifiers(dsAnalysis.DynamicsScore)
		n.logToFile(n.logFile, fmt.Sprintf("DS Modifiers for MBC - Attack: %.1fx, Release: %.1fx, Ratio: %.1fx",
			mods.AttackMultiplier, mods.ReleaseMultiplier, mods.RatioMultiplier))
	} else {
		mods = audio.CompressionModifiers{AttackMultiplier: 1.0, ReleaseMultiplier: 1.0, RatioMultiplier: 1.0}
	}

	sub := bandAnalysis["sub"]
	bass := bandAnalysis["bass"]
	lowMid := bandAnalysis["low_mid"]
	mid := bandAnalysis["mid"]
	high := bandAnalysis["high"]

	// Check if we need input attenuation for hot peaks
	var maxPeak float64 = -999.0

	// Find hottest peak across all bands
	for _, band := range []*FrequencyBandAnalysis{sub, bass, lowMid, mid, high} {
		if band != nil && band.PeakLevel > maxPeak {
			maxPeak = band.PeakLevel
		}
	}

	// Base parameters per preset
	var attackMs, releaseMs float64
	var baseRatio float64

	switch preset {
	case "Light":
		attackMs = 150
		releaseMs = 300
		baseRatio = 2.5
	case "Moderate":
		attackMs = 100
		releaseMs = 200
		baseRatio = 4.0
	case "Broadcast":
		attackMs = 10
		releaseMs = 20
		baseRatio = 6.0
	}

	// Build compression and limiting for each band
	subFilter := n.buildBandAcompressor(sub, attackMs, releaseMs, baseRatio, -18, mods)
	bassFilter := n.buildBandAcompressor(bass, attackMs, releaseMs, baseRatio, -15, mods)
	lowMidFilter := n.buildBandAcompressor(lowMid, attackMs*0.8, releaseMs*0.9, baseRatio*1.2, -12, mods)
	midFilter := n.buildBandAcompressor(mid, attackMs*0.6, releaseMs*0.7, baseRatio*1.5, -10, mods)
	highFilter := n.buildBandAcompressor(high, attackMs*0.5, releaseMs*0.6, baseRatio*2.0, -8, mods)

	// Build the complete filterchain:
	// 1. Resample to 192kHz for intersample peak accuracy
	// 2. Split into bands with acrossover
	// 3. Compress and limit each band
	// 4. Mix back together

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

	n.logToFile(n.logFile, fmt.Sprintf("Multiband filter: %s", filterChain))

	return filterChain

}

func (n *AudioNormalizer) buildBandAcompressor(band *FrequencyBandAnalysis, attackMs float64, releaseMs float64, ratio float64, fallbackThresholdDb float64, mods audio.CompressionModifiers) string {
	if band == nil {

		// Fallback compression
		thresholdLin := math.Pow(10, fallbackThresholdDb/20)
		makeup := math.Pow(10, 3.0/20) // 3dB makeup
		limiterLin := math.Pow(10, -1.0/20)

		if limiterLin > 1.0 {
			limiterLin = 1.0
		}



		return fmt.Sprintf("acompressor=threshold=%.6f:ratio=%.1f:attack=%.1f:release=%.1f:makeup=1.0,alimiter=limit=%.6f:attack=5:release=50,volume=%.3f",
			thresholdLin, ratio, attackMs, releaseMs, limiterLin, makeup)
	}

	// Calculate adaptive threshold from band peak
	var adaptiveThresholdDb float64
	if mods.RatioMultiplier < 0.3 {  // DS < 9 (Very compressed)
		// For compressed material: set threshold 2dB below peak
		adaptiveThresholdDb = band.PeakLevel - 1.0
	} else {
		// Normal material: use RMS + offset approach
		thresholdOffset := 6.0
		if mods.RatioMultiplier > 3.0 {  // DS > 21
			thresholdOffset = 3.0
		}
		adaptiveThresholdDb = band.RMSLevel + thresholdOffset
	}

	thresholdLin := math.Pow(10, adaptiveThresholdDb/20)

	// Calculate makeup gain based on expected gain reduction
	var makeupGainDb float64
	if mods.RatioMultiplier < 0.3 {  // Very compressed material
		// For DS<9, minimal/no makeup - material is already loud
		makeupGainDb = 0.0
	} else {
		// Normal material: calculate based on RMS reduction
		expectedGRDb := (band.RMSLevel - adaptiveThresholdDb) / ratio
		makeupGainDb = -expectedGRDb * 0.8
		if makeupGainDb < 0 {
			makeupGainDb = 0
		}
	}
	makeupLin := math.Pow(10, makeupGainDb/20)

	// Limiter ceiling
	var limiterCeilingDb float64

	// For very compressed material with hot peaks, raise limiter above peak
	if mods.RatioMultiplier < 0.3 {
		limiterCeilingDb = band.PeakLevel - 0.1
		if limiterCeilingDb > 0.0 {
			limiterCeilingDb = 0.0
		}
	} else {
		// Normal/dynamic material: set limiter below peak
		limiterCeilingDb = band.PeakLevel - 0.8
	}

	if limiterCeilingDb < -24.0 {
		limiterCeilingDb = -24.0
	}

	limiterLin := math.Pow(10, limiterCeilingDb/20)

	if limiterLin > 1.0 {
		limiterLin = 1.0
	}

	// Apply DS modifiers
	attackMs *= mods.AttackMultiplier
	releaseMs *= mods.ReleaseMultiplier
	ratio *= mods.RatioMultiplier

	// Scale limiter timing with DS modifiers too
	limiterAttack := 25.0 * mods.AttackMultiplier
	limiterRelease := 150.0 * mods.ReleaseMultiplier

	knee := 4.0

	// Clamp ratio minimum
	if ratio < 1.0 {
		ratio = 1.0
		knee = 1.0
	} else if ratio < 2.0 {
		knee = 2.0
	} else if ratio < 4.0 {
		knee = 3.0
	} else if ratio < 8.0 {
		knee = 4.0
	} else if ratio < 12.0 {
		knee = 6.0
	} else if ratio > 12.0 {
		knee = 7.5
	}

	if ratio > 20.0 {
		ratio = 20.0
		knee = 8.0
	}

	if thresholdLin < 0.00099 {
		thresholdLin = 0.00099
	}

	if thresholdLin > 1.0 {
		thresholdLin = 1.0
	}

	if attackMs < 0.01 {
		attackMs = 0.01
	}

	if attackMs > 2000.0 {
		attackMs = 2000.0
	}

	if releaseMs < 0.01 {
		releaseMs = 0.01
	}

	if releaseMs > 9000.0 {
		releaseMs = 9000.0
	}

	if makeupLin < 1.0 {
		makeupLin = 1.0
	}

	if makeupLin > 64.0 {
		makeupLin =64.0
	}

	if limiterAttack > 80.0 {
		limiterAttack = 80.0
	}

	if limiterRelease > 8000.0 {
		limiterRelease = 8000.0
	}

	if mods.RatioMultiplier < 0.3 {
		limiterCeilingDb = 0.0
		limiterAttack = 80.0
		limiterRelease = 2000.0
	}

	n.logToFile(n.logFile, fmt.Sprintf("Band %s: Threshold=%.1f dB, Ratio=%.1f:1, Limiter=%.1f dB, Makeup=%.1f dB",
		band.BandName, adaptiveThresholdDb, ratio, limiterCeilingDb, makeupGainDb))

	logBandComp := fmt.Sprintf("MBC: acompressor=threshold=%.6f:ratio=%.1f:attack=%.1f:release=%.1f:makeup=1.0:knee=%.1f,alimiter=limit=%.6f:attack=%.0f:release=%.0f:level=false,volume=%.3f",
	thresholdLin, ratio, attackMs, releaseMs, knee, limiterLin, limiterAttack, limiterRelease, makeupLin)

	n.logToFile(n.logFile, logBandComp)

	return fmt.Sprintf("acompressor=threshold=%.6f:ratio=%.1f:attack=%.1f:release=%.1f:makeup=1.0:knee=%1.f,alimiter=limit=%.6f:attack=%.0f:release=%.0f:level=false,volume=%.3f",
	thresholdLin, ratio, attackMs, releaseMs, knee, limiterLin, limiterAttack, limiterRelease, makeupLin)

	//return fmt.Sprintf("acompressor=threshold=%.6f:ratio=%.1f:attack=%.1f:release=%.1f:makeup=1.0:knee=6.8,volume=%.3f",
	//thresholdLin, ratio, attackMs, releaseMs, makeupLin)
}

func (n *AudioNormalizer) measureLoudnessFromFilter(inputPath string, filterChain string) map[string]string {
	n.logStatus(fmt.Sprintf("→ Measuring compressed audio: %s", filepath.Base(inputPath)))

	target := "-23"
	if n.loudnormCustomCheck.Checked && n.normalizeTarget.Text != "" {
		if strings.Contains(n.normalizeTarget.Text, "-") {
			target = n.normalizeTarget.Text
		} else {
			target = "-" + n.normalizeTarget.Text
		}
	}

	targetTp := "-1"
	if n.loudnormCustomCheck.Checked && n.normalizeTargetTp.Text != "" {
		if strings.Contains(n.normalizeTargetTp.Text, "-") {
			targetTp = n.normalizeTargetTp.Text
		} else {
			targetTp = "-" + n.normalizeTargetTp.Text
		}
	}

	cmd := exec.Command(
		ffmpegPath,
		"-i", inputPath,
		"-af", fmt.Sprintf("%s,loudnorm=linear=false:I=%s:TP=%s:LRA=5:print_format=json", filterChain, target, targetTp),
		"-f", "null",
		"-",
	)


	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}

	return n.parseLoudnormJSON(string(output))
}

func (n *AudioNormalizer) parseAstatsOutput(output string) *DynamicsAnalysis {
	result := &DynamicsAnalysis{}

	// Look for "Overall" section and parse from there
	// Format: [Parsed_astats_0 @ 0xXXXXXXXXX] Peak level dB: -65.832755

	// Extract Overall section
	overallStart := strings.Index(output, "Overall")
	if overallStart == -1 {
		return result
	}
	overallSection := output[overallStart:]

	// Parse: Peak level dB: -65.832755
	peakRe := regexp.MustCompile(`Peak level dB:\s+([-\d.]+)`)
	if match := peakRe.FindStringSubmatch(overallSection); len(match) > 1 {
		result.PeakLevel, _ = strconv.ParseFloat(match[1], 64)
	}

	// Parse: RMS peak dB: -75.013939
	rmsPeakRe := regexp.MustCompile(`RMS peak dB:\s+([-\d.]+)`)
	if match := rmsPeakRe.FindStringSubmatch(overallSection); len(match) > 1 {
		result.RMSPeak, _ = strconv.ParseFloat(match[1], 64)
	}

	// Parse: RMS trough dB: -78.685114
	rmsTroughRe := regexp.MustCompile(`RMS trough dB:\s+([-\d.]+)`)
	if match := rmsTroughRe.FindStringSubmatch(overallSection); len(match) > 1 {
		result.RMSTrough, _ = strconv.ParseFloat(match[1], 64)
	}

	// Parse: RMS level dB: -76.472639
	rmsRe := regexp.MustCompile(`RMS level dB:\s+([-\d.]+)`)
	if match := rmsRe.FindStringSubmatch(overallSection); len(match) > 1 {
		result.RMSLevel, _ = strconv.ParseFloat(match[1], 64)
	}

	// Parse from Channel 1 section (before Overall): Crest factor: 2.982689
	crestRe := regexp.MustCompile(`Crest factor:\s+([-\d.]+)`)
	if match := crestRe.FindStringSubmatch(output); len(match) > 1 {
		result.CrestFactor, _ = strconv.ParseFloat(match[1], 64)
	}

	// Parse from Channel 1 section: Dynamic range: 51.779619
	dynRe := regexp.MustCompile(`Dynamic range:\s+([-\d.]+)`)
	if match := dynRe.FindStringSubmatch(output); len(match) > 1 {
		result.DynamicRange, _ = strconv.ParseFloat(match[1], 64)
	}

	noiseFloorRe := regexp.MustCompile(`Noise floor dB:\s+([-\d.]+)`)
	if match := noiseFloorRe.FindStringSubmatch(output); len(match) > 1 {
		result.NoiseFloor, _ = strconv.ParseFloat(match[1], 64)
	}

	return result
}

func (n *AudioNormalizer) calculateAdaptiveCompression(analysis *DynamicsAnalysis, dsAnalysis *audio.DynamicsScoreAnalysis, preset string) string {
	if analysis == nil || preset == "Off" {
		return ""
	}

	var threshold, ratio, attack, release float64
	var limiterCeiling float64

	// Decide if we need limiting based on crest factor
	needsLimiting := analysis.CrestFactor > 5.0

	switch preset {
	case "Light":
		// Gentle compression on peaks
		threshold = analysis.RMSLevel + 6.0
		ratio = audio.GetBaseRatioFromCrest(analysis.CrestFactor)
		attack = 100
		release = 250
		limiterCeiling = -1.0

	case "Moderate":
		// Standard broadcast compression
		threshold = analysis.RMSLevel + 5.0
		ratio = audio.GetBaseRatioFromCrest(analysis.CrestFactor)
		attack = 40
		release = 150
		limiterCeiling = -1.0

	case "Broadcast":
		// Aggressive limiting and compression
		threshold = analysis.RMSLevel + 4.0
		ratio = audio.GetBaseRatioFromCrest(analysis.CrestFactor)
		attack = 10
		release = 30
		limiterCeiling = -1.0
	}

	// Apply DS modifiers if available
	if dsAnalysis != nil {
		mods := audio.GetCompressionModifiers(dsAnalysis.DynamicsScore)
		attack *= mods.AttackMultiplier
		release *= mods.ReleaseMultiplier
		ratio *= mods.RatioMultiplier

		n.logToFile(n.logFile, fmt.Sprintf("DS Modifiers - Attack: %.1fx, Release: %.1fx, Ratio: %.1fx",
			mods.AttackMultiplier, mods.ReleaseMultiplier, mods.RatioMultiplier))
	}

	makeupGain := calculateMakeupGain(analysis, threshold, ratio)
	thresholdLin := math.Pow(10, threshold/20)

	knee := 4.0

	if thresholdLin > 1.0 {
		thresholdLin = 1.0
	}

	if thresholdLin < 0.00099 {
		thresholdLin = 0.00099
	}

	if ratio < 1.0 {
		ratio = 1.0
		knee = 1.0
	} else if ratio < 2.0 {
		knee = 2.0
	} else if ratio < 4.0 {
		knee = 3.0
	} else if ratio < 8.0 {
		knee = 4.0
	} else if ratio < 12.0 {
		knee = 6.0
	} else if ratio > 12.0 {
		knee = 7.5
	}

	if ratio > 20.0 {
		ratio = 20.0
	}

	if attack > 2000.0 {
		attack = 2000.0
	}

	if attack < 0.01 {
		attack = 0.01
	}

	if release < 0.01 {
		release = 0.01
	}

	if release > 9000.0 {
		release = 9000.0
	}

	if makeupGain < 1.0 {
		makeupGain = 1.0
	}

	if makeupGain > 64.0 {
		makeupGain = 64.0
	}


	// Build filter chain
	var filterChain string

	// Always add compression
	filterChain = fmt.Sprintf(
		"acompressor=threshold=%.6f:ratio=%.1f:attack=%.0f:release=%.0f:knee=%.1f:makeup=%.1f",
		thresholdLin, ratio, attack, release, knee, makeupGain,
	)

	n.logToFile(n.logFile, "")
	n.logToFile(n.logFile, filterChain)
	n.logToFile(n.logFile, "")

	// Add limiter if needed
	if needsLimiting {
		limiterLinear := math.Pow(10, limiterCeiling/20)
		if limiterLinear > 1.0 {
			limiterLinear = 1.0
		}
		filterChain += fmt.Sprintf(",alimiter=limit=%.6f:attack=5:release=50", limiterLinear)
	}

	n.logToFile(n.logFile, fmt.Sprintf("Dynamics filter: %s", filterChain))

	return filterChain
}

func calculateMakeupGain(analysis *DynamicsAnalysis, threshold, ratio float64) float64 {
	// Use RMS measurements to estimate signal distribution
	rmsPeak := analysis.RMSPeak
	rmsLevel := analysis.RMSLevel

	// If threshold is above RMS peak, minimal compression happening
	if threshold >= rmsPeak {
		return 1.0  // No makeup needed, return 1.0 (unity gain)
	}

	// If threshold is below RMS level, most signal is being compressed
	var percentageAboveThreshold float64
	if threshold <= rmsLevel {
		percentageAboveThreshold = 0.7
	} else {
		thresholdPosition := (rmsPeak - threshold) / (rmsPeak - rmsLevel)
		percentageAboveThreshold = 0.3 * thresholdPosition
	}

	avgExcursion := (rmsPeak - threshold) / 2
	gainReductionPerSample := avgExcursion * ((ratio - 1) / ratio)
	effectiveGainReduction := gainReductionPerSample * percentageAboveThreshold
	makeupGainDB := effectiveGainReduction * 0.85

	// Convert dB to linear gain: 10^(dB/20)
	makeupGainLinear := math.Pow(10, makeupGainDB/20)

	// Clamp to FFmpeg's valid range [1, 64]
	if makeupGainLinear < 1.0 {
		makeupGainLinear = 1.0
	} else if makeupGainLinear > 64.0 {
		makeupGainLinear = 64.0
	}

	return makeupGainLinear
}

func (n *AudioNormalizer) getDuration(inputPath string) (float64, error) {
	cmd := ffmpeg.Command( "-i", inputPath, "-f", "null", "-")


	output, _ := cmd.CombinedOutput()
	outputStr := string(output)

	// Parse "Duration: 00:01:04.03"
	re := regexp.MustCompile(`Duration: (\d{2}):(\d{2}):(\d{2}\.\d{2})`)
	matches := re.FindStringSubmatch(outputStr)

	if len(matches) < 4 {
		return 0, fmt.Errorf("could not parse duration")
	}

	hours, _ := strconv.ParseFloat(matches[1], 64)
	minutes, _ := strconv.ParseFloat(matches[2], 64)
	seconds, _ := strconv.ParseFloat(matches[3], 64)

	totalSeconds := hours*3600 + minutes*60 + seconds
	return totalSeconds, nil
}

func (n *AudioNormalizer) calculateOutputSize(config ProcessConfig) (int64, error) {
	var totalBytes int64

	for _, file := range n.files {
		duration, err := n.getDuration(file)
		if err != nil {
			n.logToFile(n.logFile, fmt.Sprintf("Failed to get duration for %s: %v", file, err))
			continue
		}

		var fileSize int64

		if config.Format == "PCM" {
			// PCM: sample_rate × (bit_depth / 8) × channels × duration
			sampleRate, _ := strconv.ParseFloat(config.SampleRate, 64)

			var bitDepthBits float64
			switch config.BitDepth {
			case "16":
				bitDepthBits = 16
			case "24":
				bitDepthBits = 24
			case "32 (float)":
				bitDepthBits = 32
			case "64 (float)":
				bitDepthBits = 64
			default:
				bitDepthBits = 24
			}

			channels := 2.0 // Stereo
			fileSize = int64(sampleRate * (bitDepthBits / 8) * channels * duration)
		} else {
			// Lossy: (bitrate_kbps × 1000 / 8) × duration
			bitrate, _ := strconv.ParseFloat(config.Bitrate, 64)
			fileSize = int64((bitrate * 1000 / 8) * duration)
		}

		totalBytes += fileSize
	}

	return totalBytes, nil
}

func (n *AudioNormalizer) previewSize() {
	if len(n.files) == 0 {
		dialog.ShowInformation("No Files", "Please select files first", n.window)
		return
	}

	config := n.getProcessConfig()

	n.logStatus("Calculating output size...")

	go func() {
		totalBytes, err := n.calculateOutputSize(config)
		if err != nil {
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("Failed to calculate size: %v", err), n.window)
			})
			return
		}

		// Convert to human-readable format
		var sizeStr string
		if totalBytes < 1024 {
			sizeStr = fmt.Sprintf("%d B", totalBytes)
		} else if totalBytes < 1024*1024 {
			sizeStr = fmt.Sprintf("%.2f KB", float64(totalBytes)/1024)
		} else if totalBytes < 1024*1024*1024 {
			sizeStr = fmt.Sprintf("%.2f MB", float64(totalBytes)/(1024*1024))
		} else {
			sizeStr = fmt.Sprintf("%.2f GB", float64(totalBytes)/(1024*1024*1024))
		}

		fyne.Do(func() {
			n.logStatus(fmt.Sprintf("Estimated output size: %s", sizeStr))
			dialog.ShowInformation("Estimated Output Size",
				fmt.Sprintf("Total estimated size: %s\n\nBased on %d files with current settings", sizeStr, len(n.files)),
				n.window)
		})
	}()
}

type Preferences struct {
	AdvancedMode bool `json:"advanced_mode"`
	LastOutputDir string `json:"last_output_dir"`
	SimpleMode string `json:"simple_mode_selection"`
	Format string `json:"format"`
	SampleRate string `json:"sample_rate"`
	BitDepth string `json:"bit_depth"`
	Bitrate string `json:"bitrate"`
	LoudnormEnabled bool `json:"loudnorm_enabled"`
	CustomLoudnorm bool `json:"custom_loudnorm"`
	NormalizeTarget string `json:"normalize_target"`
	NormalizeTargetTp string `json:"normalize_target_tp"`
	NormalizationStandard string `json:"normalization_standard"`
	DataCompLevel int8 `json:"data_comp_level"`
	EqPreset string `json:"eq_preset"`
	DynPreset string `json:"dyn_preset"`
	DynNorm bool `json:"dyn_norm_enabled"`
	SelectedTab string `json:"selected_tab"`
	PhaseCheck bool `json:"phase_check_auto"`
}

func (n *AudioNormalizer) loadPreferences() {
	configDir, _ := os.UserConfigDir()
	prefsPath := filepath.Join(configDir, "TNT", "preferences.json")

	data, err := os.ReadFile(prefsPath)
	if err != nil {
		return
	}

	var prefs Preferences
	json.Unmarshal(data, &prefs)

	n.modeToggle.SetChecked(prefs.AdvancedMode)
	n.outputDir = prefs.LastOutputDir
	if n.outputDir != "" {
		n.outputLabel.SetText(filepath.Base(n.outputDir))
	}
	n.simpleGroupButtons.SetSelected(prefs.SimpleMode)
	n.formatSelect.SetSelected(prefs.Format)
	n.sampleRate.SetSelected(prefs.SampleRate)
	n.bitDepth.SetSelected(prefs.BitDepth)
	n.bitrateEntry.SetText(prefs.Bitrate)
	n.loudnormCheck.SetChecked(prefs.LoudnormEnabled)
	n.loudnormCustomCheck.SetChecked(prefs.CustomLoudnorm)
	n.normalizeTarget.SetText(prefs.NormalizeTarget)
	n.normalizeTargetTp.SetText(prefs.NormalizeTargetTp)
	n.normalizationStandard = prefs.NormalizationStandard
	n.updateNormalizationLabel(prefs.NormalizationStandard)
	n.dataCompLevel.SetValue(float64(prefs.DataCompLevel))
	n.EqDrop.SetSelected(prefs.EqPreset)
	n.dynamicsDrop.SetSelected(prefs.DynPreset)
	n.dynNorm.SetChecked(prefs.DynNorm)
	n.checkPhaseBtn.SetChecked(prefs.PhaseCheck)
	if prefs.SelectedTab == "Fast" {
		n.modeTabs.Select(n.modeTabs.Items[0])
	} else {
		n.modeTabs.Select(n.modeTabs.Items[1])
	}
}

func (n *AudioNormalizer) savePreferences() {
	prefs := Preferences{
		AdvancedMode: n.advancedMode,
		LastOutputDir: n.outputDir,
		SimpleMode: n.simpleGroupButtons.Selected,
		Format: n.formatSelect.Selected,
		SampleRate: n.sampleRate.Selected,
		BitDepth: n.bitDepth.Selected,
		Bitrate: n.bitrateEntry.Text,
		LoudnormEnabled: n.loudnormCheck.Checked,
		CustomLoudnorm: n.loudnormCustomCheck.Checked,
		NormalizeTarget: n.normalizeTarget.Text,
		NormalizeTargetTp: n.normalizeTargetTp.Text,
		NormalizationStandard: n.normalizationStandard,
		DataCompLevel: int8(n.dataCompLevel.Value),
		EqPreset: n.EqDrop.Selected,
		DynPreset: n.dynamicsDrop.Selected,
		DynNorm: n.dynNorm.Checked,
		SelectedTab: n.modeTabs.Selected().Text,
		PhaseCheck: n.checkPhaseBtn.Checked,
	}

	configDir, _ := os.UserConfigDir()
	prefsDir := filepath.Join(configDir, "TNT")
	os.MkdirAll(prefsDir, 0755)

	data, _ := json.MarshalIndent(prefs, "", "  ")
	os.WriteFile(filepath.Join(prefsDir, "preferences.json"), data, 0644)
}

func (n *AudioNormalizer) resetPreferences() {
	configDir, _ := os.UserConfigDir()
	prefsPath := filepath.Join(configDir, "TNT", "preferences.json")

	os.Remove(prefsPath)

	dialog.ShowInformation("Preferences Reset", "Preferences have been reset. Restart TNT to apply defaults.", n.window)
}

func (n *AudioNormalizer) updateNormalizationLabel(standard string) {
	switch standard {
		case "EBU R128 (-23 LUFS)":
			n.loudnormLabel.SetText("Normalize (EBU R128: -23 LUFS)")
			n.writeTagsLabel.SetText("Write RG tags (EBU R128: -23 LUFS)")
		case "USA ATSC A/85 (-24 LUFS)":
			n.loudnormLabel.SetText("Normalize (ATSC A/85: -24 LUFS)")
			n.writeTagsLabel.SetText("Write RG tags (ATSC A/85: -24 LUFS)")
		case "Custom":
			target := n.normalizeTarget.Text
			targetTp := n.normalizeTargetTp.Text
			n.loudnormLabel.SetText(fmt.Sprintf("Normalize (Custom %s LUFS, %s dBTP)", target, targetTp))
			n.writeTagsLabel.SetText(fmt.Sprintf("Write RG tags (Custom %s LUFS, %s dBTP)", target, targetTp))
	}
}

func (n *AudioNormalizer) startWatching() {
	n.watcherMutex.Lock()
	if n.watching {
		n.watcherMutex.Unlock()
		return
	}
	n.watching = true
	n.watcherStop = make(chan bool)
	n.jobQueue = make(chan string, 100)
	n.watcherMutex.Unlock()

	n.logStatus("Watch mode started")
	n.logToFile(n.logFile, "started watching")
	go n.watchDirectory()
	go n.processWatchQueue()
}

func (n *AudioNormalizer) stopWatching() {
	n.watcherMutex.Lock()
	defer n.watcherMutex.Unlock()

	if n.watching {
		n.watching = false
		close(n.watcherStop)
		for len(n.jobQueue) > 0 {
			<-n.jobQueue
		}
		n.logStatus("Watch mode stopped")
		n.logToFile(n.logFile, "stopped watching")
	}
}

func (n *AudioNormalizer) watchDirectory() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		n.logStatus("Failed to create watcher: " + err.Error())
		n.logToFile(n.logFile, "watcher creation fail, " + err.Error())
		return
	}
	defer watcher.Close()

	err = watcher.Add(n.inputDir)
	if err != nil {
		n.logStatus("Failed to watch directory: " + err.Error())
		n.logToFile(n.logFile, "dir creation fail, " + err.Error())
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
				n.logToFile(n.logFile, "watcher error, " + err.Error())
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
	fmt.Printf("Log file handle: %v\n", norm.logFile)
	if norm.logFile != nil {
		defer norm.logFile.Close()
		fmt.Printf("Log file path: %s\n", norm.logFile.Name())
	} else {
		fmt.Println("Failed to create log file")
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

func (n *AudioNormalizer) updateAdvancedControls() {
	isPCM := n.formatSelect.Selected == "PCM"
	isOpus := n.formatSelect.Selected == "Opus"

	if isOpus {
		n.IsSpeechCheck.Show()
		n.IsSpeechCheck.Enable()
	} else {
		n.IsSpeechCheck.Hide()
		n.IsSpeechCheck.SetChecked(false)
		n.IsSpeechCheck.Disable()
	}

	if isPCM {
		n.sampleRate.Enable()
		n.bitDepth.Enable()
		n.bitrateEntry.Hide()
		n.writeTags.Disable()
		n.writeTags.SetChecked(false)
		n.noTranscode.SetChecked(false)
		n.noTranscode.Disable()
		n.loudnormCheck.Enable()
	} else if n.loudnormCheck != nil && n.loudnormCheck.Checked {
		n.sampleRate.Disable()
		n.bitDepth.Disable()
		n.bitrateEntry.Show()
	} else {
		n.writeTags.Enable()
	}
}

func (n *AudioNormalizer) selectFiles() {
	dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		defer reader.Close()

		path := reader.URI().Path()
		if isAudioFile(path) {
			n.addFile(path)
		}
	}, n.window)
	n.batchMode = false
}

func (n *AudioNormalizer) selectFolder() {
	dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil || uri == nil {
			return
		}

		n.inputDir = uri.Path()

		n.batchMode = true

		n.logStatus("Scanning folder...")
		n.logToFile(n.logFile, "Scanning folder")

		go func() {
			audioFiles := []string{}
			filepath.WalkDir(uri.Path(), func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					return nil
				}
				if isAudioFile(path) {
					audioFiles = append(audioFiles, path)
				}
				return nil
			})

			n.mutex.Lock()
			for _, file := range audioFiles {
				// Check for duplicates inline
				exists := false

				existing := slices.Contains(n.files, file); if existing {
					exists = true
				}

				if !exists {
					n.files = append(n.files, file)
				}
			}
			n.mutex.Unlock()

			fyne.Do(func() {
				n.fileList.Refresh()
				n.updateProcessButton()
				n.logStatus(fmt.Sprintf("Added %d audio files from folder", len(audioFiles)))
			})
		}()
	}, n.window)
}

func (n *AudioNormalizer) selectOutputFolder() {
	dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil || uri == nil {
			return
		}

		n.mutex.Lock()
		n.outputDir = uri.Path()
		n.outputLabel.SetText(filepath.Base(n.outputDir))
		n.mutex.Unlock()

		n.updateProcessButton()
	}, n.window)
}

func (n *AudioNormalizer) checkPCM() bool {
	originIsPCM := false
	for _, file := range n.files {
		if strings.TrimPrefix(filepath.Ext(file), ".") == "wav" {
			originIsPCM = true
			break
		}
	}
	fyne.Do(func() {
		if originIsPCM {
			n.noTranscode.Disable()
			if n.formatSelect.Selected == "PCM" {
				n.writeTags.Disable()
				n.writeTags.SetChecked(false)
				n.noTranscode.Disable()
				n.noTranscode.SetChecked(false)
			} else {
				n.writeTags.Enable()
			}
		}
	})
	return originIsPCM
}

func (n *AudioNormalizer) checkNonTranscode() bool {
	nonTranscoding := false
	for _, file := range n.files {
		if strings.TrimPrefix(filepath.Ext(file), ".") == "ogg" {
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

func (n *AudioNormalizer) checkOriginAAC() bool {
	originIsAAC := false
	for _, file := range n.files {
		if strings.TrimPrefix(filepath.Ext(file), ".") == "m4a" {
			originIsAAC = true
			break
		}
	}
	fyne.Do(func() {
		if originIsAAC {

		}
	})
	return originIsAAC
}

func (n *AudioNormalizer) addFile(path string) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	existing := slices.Contains(n.files, path); if existing {
		return
	}

	/* OLD, above is modernized

	for _, existing := range n.files {
		if existing == path {
			return
		}
	}
	*/

	n.files = append(n.files, path)
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

func (n *AudioNormalizer) getProcessConfig() ProcessConfig {
	if n.modeTabs.Selected() == n.modeTabs.Items[0] {
		n.advancedMode = false
	} else {
		n.advancedMode = true
	}

	config := ProcessConfig{
		UseLoudnorm: n.loudnormCheck.Checked,
		IsSpeech: n.IsSpeechCheck.Checked,
		originIsAAC: n.checkOriginAAC(),
		writeTags: n.writeTags.Checked,
		noTranscode: n.noTranscode.Checked,
		dataCompLevel: int8(math.Round(n.dataCompLevel.Value)),
		bypassProc: n.bypassProc.Checked,
		DynamicsPreset: n.dynamicsDrop.Selected,
		EqTarget: n.EqDrop.Selected,
		DynNorm: n.dynNorm.Checked,
		PhaseCheck: n.checkPhaseBtn.Checked,
	}

	if n.advancedMode {
		config.Format = n.formatSelect.Selected
		config.SampleRate = n.sampleRate.Selected
		config.BitDepth = n.bitDepth.Selected
		config.Bitrate = n.bitrateEntry.Text
		config.writeTags = n.writeTags.Checked
	} else {
		switch n.simpleGroupButtons.Selected {
		case "Small file (AAC 256kbps)":
			config.Format = "AAC"
			config.Bitrate = "256"
		case "Most compatible (MP3 320kbps)":
			config.Format = "MPEG-II L3"
			config.Bitrate = "320"
		case "Production (PCM 48kHz/24bit)":
			config.Format = "PCM"
			config.SampleRate = "48000"
			config.BitDepth = "24"
		}
	}

	return config
}

func (n *AudioNormalizer) process() {
	n.processBtn.Disable()
	n.progressBar.Show()
	n.progressBar.SetValue(0)
	n.statusLog.SetText("")

	config := n.getProcessConfig()

	workers := runtime.NumCPU() - 1

	workers = max(1, workers)

	/* modernize above, old below
	if workers < 1 {
		workers = 1
	}
	*/

	// EXAMPLE REPLACEMENT PATTERN
	// 2. x = a; if a < b { x = b }                =>      x = max(a, b)


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
					shouldProcess := true

					if config.PhaseCheck {
						inverted, offset, err := audio.PhaseCheck(file, n.logFile)
						if err != nil {
							n.logStatus(fmt.Sprintf("✗ Phase check failed for %s: %v", filepath.Base(file), err))
						} else if inverted {
							n.logStatus(fmt.Sprintf("⚠ Phase inverted (offset: %.6f): %s", offset, filepath.Base(file)))

							if offset == 0 && inverted {
								shouldProcess = n.showConfirmDialog("Track is perfectly out of phase", fmt.Sprintf("%s appears to be perfectly out of phase, meaning it will render to complete silence in monophonic receivers. It is advisable to not process this file and fix the phase issue first. Do you want to process?", filepath.Base(file)))
							} else {
								// Ask on UI thread, block worker
								shouldProcess = n.showConfirmDialog(
									"Phase Inverted",
									fmt.Sprintf("%s appears phase-inverted. Continue?", filepath.Base(file)),
								)
							}
						}
					}

					if shouldProcess {
						success := n.processFile(file, config)
						results <- success
					} else {
						n.logStatus(fmt.Sprintf("⊗ Skipped: %s", filepath.Base(file)))
						results <- false
					}
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

func (n *AudioNormalizer) processFile(inputPath string, cfg ProcessConfig) bool {
	n.logToFile(n.logFile, fmt.Sprintf("DEBUG config values: EqTarget='%s', DynamicsPreset='%s', bypassProc=%v",
	cfg.EqTarget, cfg.DynamicsPreset, cfg.bypassProc))
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
	} else if cfg.writeTags && cfg.noTranscode {
		outputPath = filepath.Join(outputDir, fmt.Sprintf("%s.tagged%s", baseName, originalExt))
	} else if cfg.writeTags {
		outputPath = filepath.Join(outputDir, fmt.Sprintf("%s.tagged%s", baseName, ext))
	} else {
		outputPath = filepath.Join(outputDir, fmt.Sprintf("%s%s", baseName, ext))
	}

	n.logStatus(fmt.Sprintf("Processing: %s, outputting to %s", filepath.Base(inputPath), outputPath))

	var measured map[string]string

	// Build ffmpeg command
	args := []string{"-i", workingPath, "-vn"}

	// Add format-specific arguments
	if n.noTranscode.Checked {
		args = append(args, "-c", "copy")
	} else if actualCodec == "PCM" && !n.noTranscode.Checked {
		args = append(args, "-ar", cfg.SampleRate)

		var codec string
		switch cfg.BitDepth {
		case "16":
			codec = "pcm_s16le"
		case "24":
			codec = "pcm_s24le"
		case "32 (float)":
			codec = "pcm_f32le"
		case "64 (float)":
			codec = "pcm_f64le"
		}
		args = append(args, "-acodec", codec)
	} else if !n.noTranscode.Checked {
		args = append(args, "-ar", "48000")
		args = append(args, "-c:a", actualCodec)
	}

		needsFullNumber := (actualCodec == "libfdk_aac" || actualCodec == "aac" || actualCodec == "libopus" || actualCodec == "libmp3lame")
		noBitrateUsed := actualCodec == "PCM" || actualCodec == "flac"

		bitrateStr := cfg.Bitrate

		if needsFullNumber {
			if strings.Contains(cfg.Bitrate, "k") {
				bitrateStr = strings.ReplaceAll(cfg.Bitrate, "k", "000")
			} else if strings.Contains(cfg.Bitrate, "000") {
				bitrateStr = cfg.Bitrate
			} else {
				bitrateStr = cfg.Bitrate + "000"
			}
		}

		bitrate, err := strconv.Atoi(bitrateStr)
		minBitrate := 12
		if needsFullNumber {
			minBitrate = 12
		}
		if err != nil || bitrate <= minBitrate {
			if needsFullNumber {
				bitrate = 128000
			} else {
				bitrate = 128
			}
		}

		if !noBitrateUsed {
			if needsFullNumber {
				args = append(args, "-b:a", fmt.Sprintf("%d", bitrate))
			} else {
				args = append(args, "-b:a", fmt.Sprintf("%dk", bitrate))
			}
		}

	// Add speech optimization for Opus
	if cfg.IsSpeech && actualCodec == "libopus" && !n.noTranscode.Checked {
		args = append(args, "-application", "voip")
	} else if !cfg.IsSpeech && actualCodec == "libopus" && !n.noTranscode.Checked {
		args = append(args, "-application", "audio")
	}

	usesDataCompression := actualCodec == "flac" || actualCodec == "libopus"

	if usesDataCompression {
		var level int
		if actualCodec == "libopus" {
			level = 10 - int(cfg.dataCompLevel)
		} else if actualCodec == "flac" {
			level = int(math.Round(float64(cfg.dataCompLevel) * 12.0 / 10.0))
		}
		args = append(args, "-compression_level", fmt.Sprintf("%d", level))
	}

	// Get target from saved normalization standard
	target := "-23"
	targetTp := "-1"

	switch n.normalizationStandard {
	case "EBU R128 (-23 LUFS)":
		target = "-23"
		targetTp = "-1"
	case "USA ATSC A/85 (-24 LUFS)":
		target = "-24"
		targetTp = "-2"
	case "Custom":
		// Only use input fields when Custom is selected
		if n.normalizeTarget.Text != "" {
			if strings.Contains(n.normalizeTarget.Text, "-") {
				target = n.normalizeTarget.Text
			} else {
				target = "-" + n.normalizeTarget.Text
			}
		}
		if n.normalizeTargetTp.Text != "" {
			if strings.Contains(n.normalizeTargetTp.Text, "-") {
				targetTp = n.normalizeTargetTp.Text
			} else {
				targetTp = "-" + n.normalizeTargetTp.Text
			}
		}
	default:
		target = "-23"
		targetTp = "-1"
	}

	// Staged processing with temp files (192kHz 64-bit to prevent clipping)
	var eqFilter string
	var dynamicsFilter string
	var multibandFilter string
	var dynaudnormFilter string

	n.logToFile(n.logFile, fmt.Sprintf("DEBUG: About to check EQ section - cfg.EqTarget='%s', cfg.EqTarget != ''=%v, cfg.EqTarget != 'Off'=%v, !cfg.bypassProc=%v",
	cfg.EqTarget,
	cfg.EqTarget != "",
	cfg.EqTarget != "Off",
	!cfg.bypassProc))

	// Stage 1: EQ analysis and application
	if cfg.EqTarget != "" && cfg.EqTarget != "Off" && !cfg.bypassProc {
		eqBandAnalysis := n.analyzeFrequencyResponseBands(workingPath)
		if eqBandAnalysis == nil || len(eqBandAnalysis) == 0 {
			n.logStatus(fmt.Sprintf("✗ Failed to analyze frequency response: %s", filepath.Base(inputPath)))
			return false
		}

		n.logToFile(n.logFile, fmt.Sprintf("Frequency Response Analysis for %s:", filepath.Base(inputPath)))
		for _, band := range eqBandAnalysis {
			n.logToFile(n.logFile, fmt.Sprintf("  %s (%s): RMS=%.2f dB, Peak=%.2f dB, Crest=%.2f dB",
				band.Frequency, band.FilterType, band.RMSLevel, band.PeakLevel, band.CrestFactor))
		}

		eqFilter = n.buildEqFilter(eqBandAnalysis, cfg.EqTarget)
		n.logToFile(n.logFile, fmt.Sprintf("DEBUG: eqFilter value = '%s'", eqFilter))

		if eqFilter != "" {
			eqTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("tnt_eq_%d.wav", time.Now().UnixNano()))
			tempFiles = append(tempFiles, eqTempPath)
			n.logToFile(n.logFile, fmt.Sprintf("Added temp file: %s (total: %d)", eqTempPath, len(tempFiles)))

			n.logStatus(fmt.Sprintf("→ Applying EQ: %s", filepath.Base(inputPath)))

			fullEqFilter := eqFilter + ",deesser=i=1.0:m=1.0:f=0.05:s=o"

			cmd := ffmpeg.Command(
				"-i", workingPath,
				"-af", fullEqFilter,
				"-ar", "192000",
				"-acodec", "pcm_f64le",
				"-y", eqTempPath,
			)

			n.logToFile(n.logFile, fmt.Sprintf("%s", cmd))

			if err := cmd.Run(); err != nil {
				n.logStatus(fmt.Sprintf("✗ Failed to apply EQ: %s", filepath.Base(inputPath)))
				n.logToFile(n.logFile, fmt.Sprintf("EQ application failed: %v", err))
				return false
			}

			workingPath = eqTempPath
			n.logStatus(fmt.Sprintf("✓ EQ applied: %s", filepath.Base(inputPath)))
		}
	}

	n.logToFile(n.logFile, "")
	n.logToFile(n.logFile, "")
	n.logToFile(n.logFile, fmt.Sprintf("args: %s", args))
	n.logToFile(n.logFile, "")

	n.logToFile(n.logFile, fmt.Sprintf("DEBUG: About to check Dynamics section - cfg.DynamicsPreset='%s', cfg.DynamicsPreset != ''=%v, cfg.DynamicsPreset != 'Off'=%v, !cfg.bypassProc=%v",
	cfg.DynamicsPreset,
	cfg.DynamicsPreset != "",
	cfg.DynamicsPreset != "Off",
	!cfg.bypassProc))

	var dsAnalysis *audio.DynamicsScoreAnalysis
	if !cfg.bypassProc && (cfg.DynamicsPreset != "" && cfg.DynamicsPreset != "Off") {
		dsAnalysis = n.calculateDynamicsScore(inputPath)
		if dsAnalysis == nil {
			n.logStatus(fmt.Sprintf("✗ Failed to calculate Dynamics Score: %s", filepath.Base(inputPath)))
			return false
		}
	}

	// Stage 2: Dynaudnorm if enabled (analyze and apply to temp before loudness measurement)
	if cfg.DynNorm && !cfg.bypassProc {
		dynamicsAnalysis := n.analyzeDynamics(workingPath)
		if dynamicsAnalysis == nil {
			n.logStatus(fmt.Sprintf("✗ Failed to analyze for dynaudnorm: %s", filepath.Base(inputPath)))
			return false
		}

		dynParams := n.analyzeDynaudnormParams(dynamicsAnalysis)
		if dynParams != nil {
			dynaudnormFilter = n.buildDynaudnormFilter(dynParams)

			if dynaudnormFilter != "" {
				dynTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("tnt_dyn_%d.wav", time.Now().UnixNano()))
				tempFiles = append(tempFiles, dynTempPath)
				n.logToFile(n.logFile, fmt.Sprintf("Added temp file: %s (total: %d)", dynTempPath, len(tempFiles)))

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
					n.logToFile(n.logFile, fmt.Sprintf("Dynaudnorm application failed: %v", err))
					return false
				}

				workingPath = dynTempPath
				n.logStatus(fmt.Sprintf("✓ Dynamic normalization applied: %s", filepath.Base(inputPath)))

				// Now measure the fully processed audio for loudnorm
				if cfg.UseLoudnorm {
					measured = n.measureLoudness(workingPath)
					if measured == nil {
						n.logStatus(fmt.Sprintf("✗ Failed to measure: %s", filepath.Base(inputPath)))
						return false
					}
				}

				if cfg.writeTags {
					measured = n.measureLoudnessEbuR128(workingPath)
					if measured == nil {
						n.logStatus(fmt.Sprintf("✗ Failed to measure: %s", filepath.Base(inputPath)))
						return false
					}
				}
			}
		}
	}

	// Stage 3: Dynamics analysis and application
	if cfg.DynamicsPreset != "" && cfg.DynamicsPreset != "Off" && !cfg.bypassProc {

		// Check if MBC needs input attenuation for hot peaks
		var attenuatedPath string = workingPath
		if cfg.DynamicsPreset == "Broadcast" {
			// Quick peak check
			cmd := ffmpeg.Command( "-i", workingPath, "-af", "astats", "-f", "null", "-")

			output, _ := cmd.CombinedOutput()

			peakRe := regexp.MustCompile(`Peak level dB:\s+([-\d.]+)`)
			if match := peakRe.FindStringSubmatch(string(output)); len(match) > 1 {
				peakLevel, _ := strconv.ParseFloat(match[1], 64)

				if peakLevel > -5.0 {
					targetPeak := -6.0
					inputAttenuationDb := targetPeak - peakLevel
					inputVolumeLinear := math.Pow(10, inputAttenuationDb/20)

					attenuatedPath = filepath.Join(os.TempDir(), fmt.Sprintf("tnt_atten_%d.wav", time.Now().UnixNano()))
					tempFiles = append(tempFiles, attenuatedPath)

					n.logToFile(n.logFile, fmt.Sprintf("Hot peaks detected (%.2f dBFS), creating attenuated temp: %.2f dB", peakLevel, inputAttenuationDb))

					cmd := ffmpeg.Command(
						"-i", workingPath,
						"-af", fmt.Sprintf("volume=%.6f", inputVolumeLinear),
						"-ar", "192000",
						"-acodec", "pcm_f64le",
						"-y", attenuatedPath,
					)


					if err := cmd.Run(); err != nil {
						n.logStatus(fmt.Sprintf("✗ Failed to create attenuated temp: %s", filepath.Base(inputPath)))
						return false
					}
				}
			}
		}

		if cfg.DynamicsPreset == "Broadcast" {
			// MBC: analyze frequency bands from EQ'd file
			bandAnalysis := n.analyzeFrequencyBands(attenuatedPath)
			if bandAnalysis == nil || len(bandAnalysis) == 0 {
				n.logStatus(fmt.Sprintf("✗ Failed to analyze frequency bands: %s", filepath.Base(inputPath)))
				return false
			}
			multibandFilter = n.buildMultibandCompression(bandAnalysis, dsAnalysis, cfg.DynamicsPreset)
		} else {
			// SBC: analyze dynamics from EQ'd file
			dynamicsAnalysis := n.analyzeDynamics(workingPath)
			if dynamicsAnalysis == nil {
				n.logStatus(fmt.Sprintf("✗ Failed to analyze dynamics: %s", filepath.Base(inputPath)))
				return false
			}

			n.logToFile(n.logFile, fmt.Sprintf("Dynamics Analysis for %s:", filepath.Base(inputPath)))
			n.logToFile(n.logFile, fmt.Sprintf("  Peak Level: %.2f dBFS", dynamicsAnalysis.PeakLevel))
			n.logToFile(n.logFile, fmt.Sprintf("  RMS Peak: %.2f dBFS", dynamicsAnalysis.RMSPeak))
			n.logToFile(n.logFile, fmt.Sprintf("  RMS Trough: %.2f dBFS", dynamicsAnalysis.RMSTrough))
			n.logToFile(n.logFile, fmt.Sprintf("  RMS Level: %.2f dBFS", dynamicsAnalysis.RMSLevel))
			n.logToFile(n.logFile, fmt.Sprintf("  Crest Factor: %.2f", dynamicsAnalysis.CrestFactor))
			n.logToFile(n.logFile, fmt.Sprintf("  Dynamic Range: %.2f dB", dynamicsAnalysis.DynamicRange))

			dynamicsFilter = n.calculateAdaptiveCompression(dynamicsAnalysis, dsAnalysis, cfg.DynamicsPreset)
		}

		// Apply whichever compression filter was built
		compressionFilter := multibandFilter
		if compressionFilter == "" {
			compressionFilter = dynamicsFilter
		}

		if compressionFilter != "" {
			compTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("tnt_comp_%d.wav", time.Now().UnixNano()))
			tempFiles = append(tempFiles, compTempPath)
			n.logToFile(n.logFile, fmt.Sprintf("Added temp file: %s (total: %d)", compTempPath, len(tempFiles)))

			n.logStatus(fmt.Sprintf("→ Applying compression: %s", filepath.Base(inputPath)))

			// Use attenuatedPath if MBC created it, otherwise workingPath
			compressionInput := workingPath
			if cfg.DynamicsPreset == "Broadcast" && attenuatedPath != workingPath {
				compressionInput = attenuatedPath
			}

			cmd := ffmpeg.Command(
				"-i", compressionInput,
				"-af", compressionFilter,
				"-ar", "192000",
				"-acodec", "pcm_f64le",
				"-y", compTempPath,
			)


			if err := cmd.Run(); err != nil {
				n.logStatus(fmt.Sprintf("✗ Failed to apply compression: %s", filepath.Base(inputPath)))
				n.logToFile(n.logFile, fmt.Sprintf("Compression application failed: %v", err))
				return false
			}

			workingPath = compTempPath
			n.logStatus(fmt.Sprintf("✓ Compression applied: %s", filepath.Base(inputPath)))
		}
	}

	n.logToFile(n.logFile, "")
	n.logToFile(n.logFile, fmt.Sprintf("args: %s", args))
	n.logToFile(n.logFile, "")


	// Stage 4: Measure loudness for normalization (after all processing)
	if cfg.UseLoudnorm {
		measured = n.measureLoudness(workingPath)
		if measured == nil {
			n.logStatus(fmt.Sprintf("✗ Failed to measure: %s", filepath.Base(inputPath)))
			return false
		}
	}

	if cfg.writeTags {
		measured = n.measureLoudnessEbuR128(workingPath)
		if measured == nil {
			n.logStatus(fmt.Sprintf("✗ Failed to measure: %s", filepath.Base(inputPath)))
			return false
		}
	}

	n.logToFile(n.logFile, "")
	n.logToFile(n.logFile, fmt.Sprintf("args: %s", args))
	n.logToFile(n.logFile, "")

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

	n.logToFile(n.logFile, "")
	n.logToFile(n.logFile, "")
	n.logToFile(n.logFile, fmt.Sprintf("args: %s", args))
	n.logToFile(n.logFile, "")

	// Build final filter chain - only normalization and dynaudnorm (EQ/compression already applied to workingPath)
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
			finalFilterChain = finalFilterChain + ",aresample=resampler=soxr:dither_method=triangular"
		} else {
			finalFilterChain = "aresample=resampler=soxr:dither_method=triangular"
		}
	}

	n.logToFile(n.logFile, "")
	n.logToFile(n.logFile, "")
	n.logToFile(n.logFile, fmt.Sprintf("args: %s", args))
	n.logToFile(n.logFile, "")

	if finalFilterChain != "" {
		args = append(args, "-af", finalFilterChain)
	}

	var rgTpInLin float64

	if cfg.writeTags {
		if measured["input_tp"] == "" {
			n.logStatus("ERROR: input_tp is empty")
			rgTpInLin = 1.0  // Default value
		} else {
			rgTpFlt, err := strconv.ParseFloat(measured["input_tp"], 64)
			if err != nil {
				n.logStatus("ERROR parsing peak: " + err.Error())
				rgTpInLin = 1.0  // Default on parse error
			} else {
				rgTpInLin = math.Pow(10, rgTpFlt/20)
				n.logStatus(fmt.Sprintf("Peak in linear: %.6f", rgTpInLin))
			}
		}
	}

	resultsInM4A := (actualCodec == "libfdk_aac" || actualCodec == "aac") || (cfg.originIsAAC && cfg.noTranscode)
	useMovFlags :=  resultsInM4A && cfg.writeTags && measured != nil

	if useMovFlags {
		args = append(args, "-movflags", "use_metadata_tags")
	}

	if cfg.writeTags && measured != nil {
		inputI, _ := strconv.ParseFloat(measured["input_i"], 64)
		targetFloat, _ := strconv.ParseFloat(target, 64)
		gain := targetFloat - inputI

		args = append(args,
			"-metadata", fmt.Sprintf("REPLAYGAIN_TRACK_GAIN=%.2f dB", gain),
			"-metadata", fmt.Sprintf("REPLAYGAIN_TRACK_PEAK=%.6f", rgTpInLin),
			"-metadata", "REPLAYGAIN_REFERENCE_LOUDNESS=" + target + " LUFS",
		)
	}

	n.logToFile(n.logFile, "")
	n.logToFile(n.logFile, "")
	n.logToFile(n.logFile, "")
	n.logToFile(n.logFile, fmt.Sprintf("DEBUG args: %#v", args))
	n.logToFile(n.logFile, "")
	n.logToFile(n.logFile, "")


	args = append(args, "-y", outputPath)

	fullCmdLog := ffmpegPath + " " + strings.Join(args, " ")
	n.logToFile(n.logFile, fullCmdLog)

	cmd := ffmpeg.Command( args...)


	output, err := cmd.CombinedOutput()
	n.logToFile(n.logFile, fmt.Sprintf("FFmpeg output: %s", string(output)))

	if err != nil {
		n.logStatus(fmt.Sprintf("✗ Failed: %s - %v", filepath.Base(inputPath), err))
		n.logToFile(n.logFile, fmt.Sprintf("Failed %s - %v", filepath.Base(inputPath), err))
		n.logToFile(n.logFile, fmt.Sprintf("Error path - cleaning up %d temp files", len(tempFiles)))
		return false
	}

	if cfg.BitDepth != "" {
		n.logToFile(n.logFile, fmt.Sprintf("cfg.Bitdepth= %s", cfg.BitDepth))
	}

	if cfg.Bitrate != "" {
		n.logToFile(n.logFile, fmt.Sprintf("cfg.Bitrate= %s", cfg.Bitrate))
	}

	if cfg.SampleRate != "" {
		n.logToFile(n.logFile, fmt.Sprintf("cfg.SampleRate= %s", cfg.SampleRate))
	}

	if cfg.Format != "" {
		n.logToFile(n.logFile, fmt.Sprintf("cfg.Format= %s", cfg.Format))
	}

	if cfg.CustomLoudnorm {
		n.logToFile(n.logFile, fmt.Sprintf("Custom loudness values input and used:"))
		n.logToFile(n.logFile, fmt.Sprintf("LUFS I target: %s", target))
		n.logToFile(n.logFile, fmt.Sprintf("TP target: %s", targetTp))
	}

	if cfg.writeTags && cfg.noTranscode {
		n.logToFile(n.logFile, "Writing tags and not transcoding")
		n.logToFile(n.logFile, fmt.Sprintf("Original format is: %s", originalExt))
		n.logToFile(n.logFile, fmt.Sprintf("LUFS I target: %s", target))
		n.logToFile(n.logFile, fmt.Sprintf("TP target: %s", targetTp))
	} else if cfg.writeTags {
		n.logToFile(n.logFile, fmt.Sprintf( "Writing tags and transcoding to %s", cfg.Format))
		n.logToFile(n.logFile, fmt.Sprintf("LUFS I target: %s", target))
		n.logToFile(n.logFile, fmt.Sprintf("TP target: %s", targetTp))
	}

	n.logStatus(fmt.Sprintf("✓ Success: %s", filepath.Base(inputPath)))
	n.logToFile(n.logFile, fmt.Sprintf("✓ Success: %s", filepath.Base(inputPath)))
	n.logStatus("")
	n.logStatus(fmt.Sprintf("Your files can be found from %s. Thank you.", n.outputDir))

	n.logToFile(n.logFile, fmt.Sprintf("Cleaning up %d temp files", len(tempFiles)))
	return true
}

func (n *AudioNormalizer) parseEBUR128Output(output string) map[string]string {
	result := make(map[string]string)

	// Parse: "I:         -22.6 LUFS"
	iRe := regexp.MustCompile(`I:\s+([-\d.]+)\s+LUFS`)
	if match := iRe.FindStringSubmatch(output); len(match) > 1 {
		result["input_i"] = match[1]
	}

	// Parse: "LRA:         6.4 LU"
	lraRe := regexp.MustCompile(`LRA:\s+([-\d.]+)\s+LU`)
	if match := lraRe.FindStringSubmatch(output); len(match) > 1 {
		result["input_lra"] = match[1]
	}

	// Parse: "Threshold: -34.1 LUFS"
	threshRe := regexp.MustCompile(`Threshold:\s+([-\d.]+)\s+LUFS`)
	if match := threshRe.FindStringSubmatch(output); len(match) > 1 {
		result["input_thresh"] = match[1]
	}

	// Parse: "Peak: n.y dBFS"
	pkRe := regexp.MustCompile(`Peak:\s+([-\d.]+)\s+dBFS`)
	if match := pkRe.FindStringSubmatch(output); len(match) > 1 {
		result["input_tp"] = match[1]
	}

	n.logStatus(result["input_i"])
	n.logStatus(result["input_lra"])
	n.logStatus(result["input_thresh"])
	n.logStatus(result["input_tp"])

	return result
}

func (n *AudioNormalizer) measureLoudnessEbuR128(inputPath string) map[string]string {
	cmd := exec.Command(
		ffmpegPath,
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

	cmd := exec.Command(
		ffmpegPath,
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

func (n *AudioNormalizer) parseLoudnormJSON(output string) map[string]string {
	// Find JSON block in output
	re := regexp.MustCompile(`(?s)\{[^\}]*"input_i"[^\}]*\}`)
	jsonMatch := re.FindString(output)

	if jsonMatch == "" {
		return nil
	}

	n.logStatus(fmt.Sprintf("Measured JSON: %s", jsonMatch))

	var data map[string]any
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

	acceptedExt := slices.Contains(audioExts, ext); if acceptedExt {
		return true
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
			return color.RGBA{R: 0x14, G: 0x1e, B: 0x30, A: 0xff} // Navy
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
			return color.RGBA{R: 0x0f, G: 0x16, B: 0x24, A: 0xff} // Darker navy
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
	case theme.ColorNamePlaceHolder:
		return color.RGBA{R: 0x8e, G: 0x8e, B: 0x93, A: 0xff}
	case theme.ColorNamePressed:
		return color.RGBA{R: 0xc8, G: 0x60, B: 0x63, A: 0xff}
	case theme.ColorNameSelection:
		return color.RGBA{R: 0xde, G: 0x79, B: 0x7c, A: 0x66}
	case theme.ColorNameMenuBackground:
		return color.RGBA{R: 0xeb, G: 0xeb, B: 0xeb, A: 0xff}
	case theme.ColorNameOverlayBackground:
		return color.RGBA{R: 0xeb, G: 0xeb, B: 0xeb, A: 0xff}
	case theme.ColorNameDisabled:
		return color.RGBA{R: 0x99, G: 0x99, B: 0x99, A: 0xff}
	default:
		return theme.DefaultTheme().Color(name, variant)
	}
}

func (a *appleTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (a *appleTheme) Font(style fyne.TextStyle) fyne.Resource {
	return resourceWotfardRegularTtf
}

func (a *appleTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 8
	case theme.SizeNameInlineIcon:
		return 20
	case theme.SizeNameScrollBar:
		return 12
	default:
		return theme.DefaultTheme().Size(name)
	}
}

func cleanupTempFiles(files []string) {
	for _, file := range files {
		if err := os.Remove(file); err != nil {
			// Log but don't fail - cleanup is best-effort
			fmt.Printf("Failed to remove temp file %s: %v\n", file, err)
		}
	}
}
