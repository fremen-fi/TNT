package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
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

	"github.com/fsnotify/fsnotify"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/fremen-fi/tnt/go/audio"
	"github.com/fremen-fi/tnt/go/internal/config"
	"github.com/fremen-fi/tnt/go/internal/ffmpeg"
	"github.com/fremen-fi/tnt/go/internal/telemetry"
)

const versionCheckURL = "https://frm-sw-storage.s3.rbx.io.cloud.ovh.net/tnt-version.json"

// currentVersion is the version of this build. It is injected at link time from
// the git tag by the release workflow (-X main.currentVersion=...). A plain
// `go build` leaves it as "dev", which makes un-stamped builds obvious.
var currentVersion = "devil"

// PlatformRelease is the per-platform release entry inside the manifest. Each
// supported platform tracks its own version independently, so a platform-only
// release (e.g. a darwin/arm64 build) bumps just that platform.
type PlatformRelease struct {
	Version            string `json:"version"`
	DownloadURL        string `json:"download_url"`
	SupportedPlatforms string `json:"supported_platforms"`
	ReleaseNotes       string `json:"release_notes"`
	ReleaseDate        string `json:"release_date"`
}

// VersionManifest is the full tnt-version.json document.
type VersionManifest struct {
	Platforms map[string]PlatformRelease `json:"platforms"`
	History   []map[string]string        `json:"history"`
}

// VersionInfo is what gets handed to the frontend: the release for the running
// platform, flattened with the platform key it was selected by.
type VersionInfo struct {
	Platform           string `json:"platform"`
	Version            string `json:"version"`
	DownloadURL        string `json:"download_url"`
	SupportedPlatforms string `json:"supported_platforms"`
	ReleaseNotes       string `json:"release_notes"`
	ReleaseDate        string `json:"release_date"`
}

// platformKey maps the running OS/arch to a key in the manifest's platforms map.
// darwin is split by architecture (Apple Silicon vs Intel); other OSes have a
// single distributed build.
func platformKey() string {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "darwin-arm64"
		}
		return "darwin-amd64"
	case "windows":
		return "windows"
	default:
		return "linux"
	}
}

// Global ASC values
const (
	globalAscOn    = "true"
	globalAscLevel = "0.5"
)

// AudioNormalizer struct moved to app.go (Wails migration — Phase 1).
type ProcessConfig struct {
	Format         string
	SampleRate     string
	BitDepth       string
	Bitrate        string
	UseLoudnorm    bool
	CustomLoudnorm bool
	// Custom loudness targets entered in the Advanced view. Sent with every
	// Process request so the backend uses the live UI value, not whatever
	// Preferences last persisted. Empty when the field is blank.
	NormalizeTarget   string
	NormalizeTargetTp string
	IsSpeech          bool
	WriteTags         bool
	NoTranscode       bool
	OriginIsAAC       bool
	DataCompLevel     int8
	DynamicsPreset    string
	BypassProc        bool
	EqTarget          string
	DynNorm           bool
	PhaseCheck        bool
}

// DynamicsAnalysis and FrequencyBandAnalysis are aliases for the public audio
// package types so app code keeps using the short names.
type DynamicsAnalysis = audio.DynamicsAnalysis

type FrequencyBandAnalysis = audio.FrequencyBandAnalysis

// checkForUpdates fetches the latest version info; the caller decides what to
// do with the result (Phase 1 stub — Wails frontend will hook this up via an
// event in Phase 2).
func checkForUpdates(currentVersion string, logFile *LogIntoFile, notify func(VersionInfo)) {
	logFile.Write("Starting update check...")
	time.Sleep(500 * time.Millisecond)

	logFile.Write("Fetching version info from server...")
	resp, err := http.Get(versionCheckURL)
	if err != nil {
		logFile.Write(fmt.Sprintf("HTTP error: %v", err))
		return
	}
	defer resp.Body.Close()

	logFile.Write("Parsing JSON...")
	var manifest VersionManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		logFile.Write(fmt.Sprintf("JSON decode error: %v", err))
		return
	}

	key := platformKey()
	release, ok := manifest.Platforms[key]
	if !ok {
		logFile.Write(fmt.Sprintf("No release entry for platform %q", key))
		return
	}

	logFile.Write(fmt.Sprintf("Platform: %s, Current: %s, Remote: %s", key, currentVersion, release.Version))
	comparison := compareVersions(release.Version, currentVersion)
	logFile.Write(fmt.Sprintf("Comparison result: %d", comparison))

	if comparison > 0 {
		logFile.Write("Update available")
		if notify != nil {
			notify(VersionInfo{
				Platform:           key,
				Version:            release.Version,
				DownloadURL:        release.DownloadURL,
				SupportedPlatforms: release.SupportedPlatforms,
				ReleaseNotes:       release.ReleaseNotes,
				ReleaseDate:        release.ReleaseDate,
			})
		}
	} else {
		logFile.Write("Already up to date")
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

var ffmpegPath string

func init() {
	ffmpegPath = ffmpeg.Path
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

type LogIntoFile struct {
	logFile *os.File
}
type LogApp struct {
	ctx context.Context
}

func (f *LogIntoFile) Write(message string) {
	if f.logFile != nil {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		f.logFile.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, message))
	}
}

func (f *LogIntoFile) Close() {
	if f.logFile != nil {
		f.logFile.Close()
	}
}

func (a *LogApp) Write(message string) {
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, "status:log", message)
}

func (n *AudioNormalizer) sendLogReport() {
	configDir, _ := os.UserConfigDir()
	logPath := filepath.Join(configDir, "TNT", "tnt.log")

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		n.appLog.Write("No log file found. Try processing some files first.")
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
				n.appLog.Write(fmt.Sprintf("Failed to launch email client: %v", err))
			}
		} else if err := cmd.Run(); err != nil {
			n.appLog.Write(fmt.Sprintf("Failed to open email client. Log file location:\n%s", logPath))
		}
	}

	if runtime.GOOS == "windows" && copyLocation != "" {
		n.appLog.Write(fmt.Sprintf("Log file copied to your Desktop:\n%s\n\nPlease attach it to the email. If no native email client was found, none was opened. In this case, send the email manually.", filepath.Base(copyLocation)))
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
		n.logFile.Write(fmt.Sprintf("astats failed: %v", err))
		return nil
	}

	// LOG THE RAW OUTPUT
	//n.logFile.Write("=== RAW ASTATS OUTPUT START ===")
	//n.logFile.Write(string(output))
	//n.logFile.Write("=== RAW ASTATS OUTPUT END ===")

	return n.parseAstatsOutput(string(output))
}

// analyzeFrequencyBands runs the 5 multiband (sub/bass/low_mid/mid/high)
// astats passes concurrently. Each pass is an independent ffmpeg
// invocation reading inputPath; concurrency is bounded at GOMAXPROCS.
// Per-band logging happens after the parallel wait so log ordering
// matches the canonical band order rather than the goroutine schedule.
func (n *AudioNormalizer) analyzeFrequencyBands(inputPath string) map[string]*FrequencyBandAnalysis {
	bands := audio.FrequencyBandFilters()

	n.logFile.Write(fmt.Sprintf("=== FREQUENCY BAND ANALYSIS START: %s ===", filepath.Base(inputPath)))

	type bandResult struct {
		analysis *FrequencyBandAnalysis
		err      error
	}
	results := make(map[string]bandResult, len(bands))
	var mu sync.Mutex

	maxParallel := runtime.GOMAXPROCS(0)
	maxParallel = max(1, maxParallel)
	maxParallel = min(maxParallel, len(bands))
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup

	for bandName, filter := range bands {
		wg.Add(1)
		sem <- struct{}{}
		wg.Go(func() {
			defer wg.Done()
			defer func() { <-sem }()
			cmd := exec.Command(
				ffmpegPath,
				"-i", inputPath,
				"-af", fmt.Sprintf("%s,astats", filter),
				"-f", "null",
				"-",
			)
			out, err := cmd.CombinedOutput()
			r := bandResult{}
			if err != nil {
				r.err = err
			} else {
				r.analysis = n.parseFrequencyBandOutput(string(out), bandName)
			}
			mu.Lock()
			results[bandName] = r
			mu.Unlock()
		})
	}
	wg.Wait()

	// Log in canonical band order, then assemble the result map for the
	// caller. Ordering here is purely cosmetic for the log file.
	out := make(map[string]*FrequencyBandAnalysis, len(results))
	for _, name := range []string{"sub", "bass", "low_mid", "mid", "high"} {
		r, ok := results[name]
		if !ok {
			continue
		}
		if r.err != nil {
			n.logFile.Write(fmt.Sprintf("Band %s analysis failed: %v", name, r.err))
			continue
		}
		if r.analysis == nil {
			continue
		}
		out[name] = r.analysis
		n.logFile.Write(fmt.Sprintf("Band %s Results:", name))
		n.logFile.Write(fmt.Sprintf("  Peak: %.2f dBFS", r.analysis.PeakLevel))
		n.logFile.Write(fmt.Sprintf("  RMS: %.2f dBFS", r.analysis.RMSLevel))
		n.logFile.Write(fmt.Sprintf("  Crest: %.2f", r.analysis.CrestFactor))
	}

	n.logFile.Write("=== FREQUENCY BAND ANALYSIS END ===")
	return out
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

	return result
}

func (n *AudioNormalizer) buildMultibandCompression(bandAnalysis map[string]*FrequencyBandAnalysis, dsAnalysis *audio.DynamicsScoreAnalysis, preset string) string {
	if len(bandAnalysis) == 0 || preset == "Off" {
		return ""
	}

	n.logFile.Write(fmt.Sprintf("=== BUILDING MULTIBAND COMPRESSION (%s) ===", preset))

	var mods audio.CompressionModifiers
	if dsAnalysis != nil {
		mods = audio.GetCompressionModifiers(dsAnalysis.DynamicsScore)
		n.logFile.Write(fmt.Sprintf("DS Modifiers for MBC - Attack: %.1fx, Release: %.1fx, Ratio: %.1fx",
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
		attackMs = 40
		releaseMs = 80
		baseRatio = 5.0
	}

	// Build compression and limiting for each band
	subFilter := n.buildBandAcompressor(sub, attackMs, releaseMs, baseRatio, -18, mods)
	bassFilter := n.buildBandAcompressor(bass, attackMs, releaseMs, baseRatio, -15, mods)
	lowMidFilter := n.buildBandAcompressor(lowMid, attackMs*0.8, releaseMs*0.9, baseRatio*1.2, -12, mods)
	midFilter := n.buildBandAcompressor(mid, attackMs*1., releaseMs*0.8, baseRatio*1.5, -10, mods)
	highFilter := n.buildBandAcompressor(high, attackMs*2., releaseMs*0.6, baseRatio*0.2, -8, mods)

	// Build the complete filterchain:
	// 1. Resample to 192kHz for intersample peak accuracy
	// 2. Split into bands with acrossover
	// 3. Compress and limit each band
	// 4. Mix back together

	// Pre-refactor this filter was the only thing in its own ffmpeg
	// invocation and led with the upsample. With the unified filtergraph
	// the chain owns the single leading aresample=192000, so we omit it
	// here; FilterChain.Add also defends against accidental duplicates.
	filterChain := ""

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

	n.logFile.Write(fmt.Sprintf("Multiband filter: %s", filterChain))

	return filterChain

}

func (n *AudioNormalizer) buildBandAcompressor(band *FrequencyBandAnalysis, attackMs float64, releaseMs float64, ratio float64, fallbackThresholdDb float64, mods audio.CompressionModifiers) string {
	if band == nil {

		// Fallback compression
		thresholdLin := math.Pow(10, fallbackThresholdDb/20)
		makeup := math.Pow(10, 3.0/20) // 3dB makeup
		limiterLin := math.Pow(10, -1.0/20)

		limiterLin = min(limiterLin, 1.0)

		return fmt.Sprintf("acompressor=threshold=%.6f:ratio=%.1f:attack=%.1f:release=%.1f:makeup=1.0,alimiter=limit=%.6f:attack=15:release=50,volume=%.3f",
			thresholdLin, ratio, attackMs, releaseMs, limiterLin, makeup)
	}

	// Calculate adaptive threshold from band peak
	var adaptiveThresholdDb float64
	if mods.RatioMultiplier < 0.3 { // DS < 9 (Very compressed)
		// For compressed material: set threshold 2dB below peak
		adaptiveThresholdDb = band.PeakLevel - 1.0
	} else {
		// Normal material: use RMS + offset approach
		thresholdOffset := 6.0
		if mods.RatioMultiplier > 3.0 { // DS > 21
			thresholdOffset = 3.0
		}
		adaptiveThresholdDb = band.RMSLevel + thresholdOffset
	}

	thresholdLin := math.Pow(10, adaptiveThresholdDb/20)

	// Calculate makeup gain based on expected gain reduction
	var makeupGainDb float64
	if mods.RatioMultiplier < 0.3 { // Very compressed material
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

	switch {
	case ratio < 1.0:
		ratio = 1.0
		knee = 1.0
	case ratio < 2.0:
		knee = 2.0
	case ratio < 4.0:
		knee = 3.0
	case ratio < 8.0:
		knee = 4.0
	case ratio < 12.0:
		knee = 6.0
	case ratio < 16.0:
		knee = 7.5
	case ratio > 20.0:
		ratio = 20.0
		knee = 8.0
	default:
		knee = 1.4
	}

	// clamps
	thresholdLin = max(min(thresholdLin, 1.0), 0.00099)
	attackMs = max(min(attackMs, 2000.0), 0.01)
	releaseMs = max(min(releaseMs, 9000.0), 0.01)
	makeupLin = max(min(makeupLin, 64.0), 1.0)
	limiterAttack = max(min(limiterAttack, 80.0), 0.1)
	limiterRelease = max(min(limiterRelease, 8000.0), 1.0)

	if mods.RatioMultiplier < 0.3 {
		limiterCeilingDb = 0.0
		limiterAttack = 80.0
		limiterRelease = 2000.0
	}

	n.logFile.Write(fmt.Sprintf("Band %s: Threshold=%.1f dB, Ratio=%.1f:1, Limiter=%.1f dB, Makeup=%.1f dB",
		band.BandName, adaptiveThresholdDb, ratio, limiterCeilingDb, makeupGainDb))

	logBandComp := fmt.Sprintf("MBC: acompressor=threshold=%.6f:ratio=%.1f:attack=%.1f:release=%.1f:makeup=1.0:knee=%.1f,alimiter=limit=%.6f:attack=%.0f:release=%.0f:level=false,volume=%.3f",
		thresholdLin, ratio, attackMs, releaseMs, knee, limiterLin, limiterAttack, limiterRelease, makeupLin)

	n.logFile.Write(logBandComp)

	return fmt.Sprintf("acompressor=threshold=%.6f:ratio=%.1f:attack=%.1f:release=%.1f:makeup=1.0:knee=%.1f,alimiter=limit=%.6f:attack=%.0f:release=%.0f:level=false,volume=%.3f",
		thresholdLin, ratio, attackMs, releaseMs, knee, limiterLin, limiterAttack, limiterRelease, makeupLin)

	//return fmt.Sprintf("acompressor=threshold=%.6f:ratio=%.1f:attack=%.1f:release=%.1f:makeup=1.0:knee=6.8,volume=%.3f",
	//thresholdLin, ratio, attackMs, releaseMs, makeupLin)
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
		threshold = analysis.RMSLevel + 5.0
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

		n.logFile.Write(fmt.Sprintf("DS Modifiers - Attack: %.1fx, Release: %.1fx, Ratio: %.1fx",
			mods.AttackMultiplier, mods.ReleaseMultiplier, mods.RatioMultiplier))
	}

	makeupGain := calculateMakeupGain(analysis, threshold, ratio)
	thresholdLin := math.Pow(10, threshold/20)

	knee := 4.0

	thresholdLin = max(min(thresholdLin, 1.0), 0.00099)

	switch {
	case ratio < 1.0:
		ratio = 1.0
		knee = 1.0
	case ratio < 2.0:
		knee = 2.0
	case ratio < 4.0:
		knee = 3.0
	case ratio < 8.0:
		knee = 4.0
	case ratio < 12.0:
		knee = 6.0
	case ratio < 16.0:
		knee = 7.5
	case ratio > 20.0:
		ratio = 20.0
		knee = 8.0
	default:
		knee = 1.4
	}

	ratio = max(min(ratio, 20.0), 1.0)
	attack = max(min(attack, 2000.0), 0.01)
	release = max(min(release, 9000.0), 0.01)
	makeupGain = max(min(makeupGain, 64.0), 1.0)

	// Build filter chain
	var filterChain string

	// Always add compression
	filterChain = fmt.Sprintf(
		"acompressor=threshold=%.6f:ratio=%.1f:attack=%.0f:release=%.0f:knee=%.1f:makeup=%.1f",
		thresholdLin, ratio, attack, release, knee, makeupGain,
	)

	n.logFile.Write("")
	n.logFile.Write(filterChain)
	n.logFile.Write("")

	// Add limiter if needed
	if needsLimiting {
		limiterLinear := math.Pow(10, limiterCeiling/20)
		if limiterLinear > 1.0 {
			limiterLinear = 1.0
		}
		filterChain += fmt.Sprintf(",alimiter=limit=%.6f:attack=5:release=50:asc=%s:asc_level=%s:level=false", limiterLinear, globalAscOn, globalAscLevel)
	}

	n.logFile.Write(fmt.Sprintf("Dynamics filter: %s", filterChain))

	return filterChain
}

func calculateMakeupGain(analysis *DynamicsAnalysis, threshold, ratio float64) float64 {
	// Use RMS measurements to estimate signal distribution
	rmsPeak := analysis.RMSPeak
	rmsLevel := analysis.RMSLevel

	// If threshold is above RMS peak, minimal compression happening
	if threshold >= rmsPeak {
		return 1.0 // No makeup needed, return 1.0 (unity gain)
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
	cmd := ffmpeg.Command("-i", inputPath, "-f", "null", "-")

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
			n.logFile.Write(fmt.Sprintf("Failed to get duration for %s: %v", file, err))
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
		n.appLog.Write("Please select files first")
		return
	}

	config := n.getProcessConfig()

	n.appLog.Write("Calculating output size...")

	go func() {
		totalBytes, err := n.calculateOutputSize(config)
		if err != nil {
			n.appLog.Write(fmt.Sprintf("Failed to calculate size: %v", err))
			return
		}

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

		n.appLog.Write(fmt.Sprintf("Estimated output size: %s (%d files)", sizeStr, len(n.files)))
	}()
}

type Preferences struct {
	AdvancedMode          bool     `json:"advanced_mode"`
	LastOutputDir         string   `json:"last_output_dir"`
	SimpleMode            string   `json:"simple_mode_selection"`
	Format                string   `json:"format"`
	SampleRate            string   `json:"sample_rate"`
	BitDepth              string   `json:"bit_depth"`
	Bitrate               string   `json:"bitrate"`
	LoudnormEnabled       bool     `json:"loudnorm_enabled"`
	CustomLoudnorm        bool     `json:"custom_loudnorm"`
	NormalizeTarget       string   `json:"normalize_target"`
	NormalizeTargetTp     string   `json:"normalize_target_tp"`
	NormalizationStandard Standard `json:"normalization_standard"`
	DataCompLevel         int8     `json:"data_comp_level"`
	EqPreset              string   `json:"eq_preset"`
	DynPreset             string   `json:"dyn_preset"`
	DynNorm               bool     `json:"dyn_norm_enabled"`
	SelectedTab           string   `json:"selected_tab"`
	PhaseCheck            bool     `json:"phase_check_auto"`
	TelemetryEnabled      bool     `json:"telemetry_enabled"`
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
}

func (n *AudioNormalizer) savePreferences() {
	prefs := Preferences{
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

	n.appLog.Write("Preferences have been reset. Restart TNT to apply defaults.")
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

	n.appLog.Write("Watch mode started")
	n.logFile.Write("started watching")
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
		n.appLog.Write("Watch mode stopped")
		n.logFile.Write("stopped watching")
	}
}

func (n *AudioNormalizer) watchDirectory() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		n.appLog.Write("Failed to create watcher: " + err.Error())
		n.logFile.Write("watcher creation fail, " + err.Error())
		return
	}
	defer watcher.Close()

	err = watcher.Add(n.inputDir)
	if err != nil {
		n.appLog.Write("Failed to watch directory: " + err.Error())
		n.logFile.Write("dir creation fail, " + err.Error())
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
			n.appLog.Write("Watcher error: " + err.Error())
			n.logFile.Write("watcher error, " + err.Error())
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

// main is a Phase-1 stub. Phase 2 rewrites this against wails.Run().
func main() {
	if cliCfg, isCLI := parseCLIFlags(); isCLI {
		cliMode = true
		runCLI(cliCfg)
		return
	}

	norm := &AudioNormalizer{
		files:                 make([]string, 0),
		normalizationStandard: EBU,
	}

	frontendFS, fsErr := fs.Sub(assets, "frontend")
	if fsErr != nil {
		fmt.Fprintf(os.Stderr, "embed sub: %v\n", fsErr)
		os.Exit(1)
	}

	err := wails.Run(&options.App{
		Title:     "TNT — Transcode, Normalize, Tag",
		Width:     850,
		Height:    700,
		MinWidth:  750,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: frontendFS,
		},
		OnStartup: func(ctx context.Context) {
			norm.ctx = ctx
			norm.logFile = &LogIntoFile{logFile: norm.initLogFile()}
			norm.appLog = &LogApp{ctx: ctx}
			norm.loadPreferences()

			norm.telemetry = telemetry.New(currentVersion)
			norm.telemetry.SetEnabled(norm.telemetryEnabled)
			norm.telemetry.Start()
			ffmpeg.Recorder = func(args []string, output []byte, exitOK bool, dur time.Duration) {
				norm.telemetry.FFmpegRun(args, output, exitOK, dur)
			}
			norm.telemetry.AppOpen()

			go checkForUpdates(currentVersion, norm.logFile, func(v VersionInfo) {
				wailsruntime.EventsEmit(ctx, "update:available", v)
			})
		},
		OnShutdown: func(ctx context.Context) {
			norm.savePreferences()
			if norm.telemetry != nil {
				norm.telemetry.Stop()
			}
			if norm.logFile != nil {
				norm.logFile.Close()
			}
		},
		Bind: []any{norm},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "wails.Run: %v\n", err)
		os.Exit(1)
	}
}

func (n *AudioNormalizer) checkOriginAAC() bool {
	for _, file := range n.files {
		ext := strings.TrimPrefix(filepath.Ext(file), ".")
		if ext == "m4a" || ext == "aac" {
			return true
		}
	}
	return false
}

func (n *AudioNormalizer) addFile(path string) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	existing := slices.Contains(n.files, path)
	if existing {
		return
	}

	n.files = append(n.files, path)
}

func (n *AudioNormalizer) getProcessConfig() ProcessConfig {
	config := ProcessConfig{
		UseLoudnorm:    n.useLoudnorm,
		IsSpeech:       n.isSpeech,
		OriginIsAAC:    n.checkOriginAAC(),
		WriteTags:      n.writeTags,
		NoTranscode:    n.noTranscode,
		DataCompLevel:  n.dataCompLevel,
		BypassProc:     n.bypassProc,
		DynamicsPreset: n.dynamicsPreset,
		EqTarget:       n.eqPreset,
		DynNorm:        n.dynNorm,
		PhaseCheck:     n.phaseCheck,
		CustomLoudnorm: n.customLoudnorm,
		Format:         n.format,
		SampleRate:     n.sampleRate,
		BitDepth:       n.bitDepth,
		Bitrate:        n.bitrate,
	}

	return config
}

func (n *AudioNormalizer) process() {
	n.emitProgress(0)

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

	n.appLog.Write(fmt.Sprintf("Processing %d files with %d workers...", len(n.files), workers))

	go func() {
		jobs := make(chan string, len(n.files))
		results := make(chan bool, len(n.files))

		var wg sync.WaitGroup

		for i := 0; i < workers; i++ {
			wg.Add(1)
			wg.Go(func() {
				defer wg.Done()
				for file := range jobs {
					shouldProcess := true

					if config.PhaseCheck {
						inverted, offset, err := audio.PhaseCheck(ffmpeg.Path, file)
						if err != nil {
							n.appLog.Write(fmt.Sprintf("✗ Phase check failed for %s: %v", filepath.Base(file), err))
						} else if inverted {
							n.appLog.Write(fmt.Sprintf("⚠ Phase inverted (offset: %.6f): %s", offset, filepath.Base(file)))

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
						n.appLog.Write(fmt.Sprintf("⊗ Skipped: %s", filepath.Base(file)))
						results <- false
					}
				}
			})
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
			n.emitProgress(progress)
		}

		n.appLog.Write(fmt.Sprintf("\nComplete: %d/%d files processed successfully", successful, len(n.files)))
		n.emitDone()
	}()
}

func (n *AudioNormalizer) processFile(inputPath string, cfg ProcessConfig) bool {
	n.logFile.Write(fmt.Sprintf("DEBUG config values: EqTarget='%s', DynamicsPreset='%s', bypassProc=%v",
		cfg.EqTarget, cfg.DynamicsPreset, cfg.BypassProc))
	actualCodec := cfg.Format
	var workingPath string = inputPath
	var tempFiles []string
	// dev: don't delete temps for testing purposes
	// defer func() { cleanupTempFiles(tempFiles) }()

	if platformCodec := platformCodecMap[cfg.Format]; platformCodec != "" {
		actualCodec = platformCodec
	} else if codec := config.GetCodec(cfg.Format); codec != "" {
		actualCodec = codec
	}

	n.logFile.Write(fmt.Sprintf("DEBUG: cfg.Format=%s, actualCodec=%s", cfg.Format, actualCodec))

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
	} else {
		outputPath = filepath.Join(outputDir, fmt.Sprintf("%s%s", baseName, ext))
	}

	n.appLog.Write(fmt.Sprintf("Processing: %s, outputting to %s", filepath.Base(inputPath), outputPath))

	var measured map[string]string

	// Resolve the final output sample rate. PCM and FLAC honour the user-selected
	// rate; lossy codecs use 48000 (Opus only supports 48 kHz anyway). This is the
	// rate the internal oversampling rate is derived from, and the rate of the
	// single closing downsample.
	outputRate := 48000
	if actualCodec == "PCM" || actualCodec == "flac" {
		if r, e := strconv.Atoi(strings.TrimSpace(cfg.SampleRate)); e == nil && r > 0 {
			outputRate = r
		}
	}

	// Build ffmpeg command
	args := []string{"-i", workingPath, "-vn"}

	// Add format-specific arguments
	if n.noTranscode {
		args = append(args, "-c", "copy")
	} else if actualCodec == "PCM" {
		args = append(args, "-ar", strconv.Itoa(outputRate))

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
	} else if actualCodec == "flac" {
		args = append(args, "-ar", strconv.Itoa(outputRate))
		args = append(args, "-c:a", actualCodec)
	} else {
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

	if !noBitrateUsed && !n.noTranscode {
		if needsFullNumber {
			args = append(args, "-b:a", fmt.Sprintf("%d", bitrate))
		} else {
			args = append(args, "-b:a", fmt.Sprintf("%dk", bitrate))
		}
	}

	// Add speech optimization for Opus
	if cfg.IsSpeech && actualCodec == "libopus" && !n.noTranscode {
		args = append(args, "-application", "voip")
	} else if !cfg.IsSpeech && actualCodec == "libopus" && !n.noTranscode {
		args = append(args, "-application", "audio")
	}

	usesDataCompression := (actualCodec == "flac" || actualCodec == "libopus") && !n.noTranscode

	if usesDataCompression {
		var level int
		if actualCodec == "libopus" {
			level = 10 - int(cfg.DataCompLevel)
		} else if actualCodec == "flac" {
			level = int(math.Round(float64(cfg.DataCompLevel) * 12.0 / 10.0))
		}
		args = append(args, "-compression_level", fmt.Sprintf("%d", level))
	}

	// Get target from saved normalization standard
	target := -23.
	targetTp := -1.

	/*
		switch n.normalizationStandard {
		case "EBU R128 (-23 LUFS)":
			target = "-23"
			targetTp = "-1"
		case "USA ATSC A/85 (-24 LKFS)":
			target = "-24"
			targetTp = "-2"
		case "AES77-2023 (-16/-18 LUFS)": // The standard specifies -16 LUFS for music, and speech, when measurable, should be attenuated by 2 LU.
			target = "-16"  // not actually used, since overwritten below
			targetTp = "-1" // as above
		case "Custom":
			// Only use input fields when Custom is selected
			if n.normalizeTarget != "" {
				if strings.Contains(n.normalizeTarget, "-") {
					target = n.normalizeTarget
				} else {
					target = "-" + n.normalizeTarget
				}
			}
			if n.normalizeTargetTp != "" {
				if strings.Contains(n.normalizeTargetTp, "-") {
					targetTp = n.normalizeTargetTp
				} else {
					targetTp = "-" + n.normalizeTargetTp
				}
			}
		default:
			target = "-23"
			targetTp = "-1"
		}

		// Check for speech-specific standards
		// Overwrites the values set by case above
		// AES77-2023 defines a target level of -16 LUFS, -1 dB TP for music,
		// and if speech is measurable separately, it shall be attenuated by 2 LU,
		// hence the target -18 LUFS for speech
		if n.normalizationStandard == "AES77-2023 (-16/-18 LUFS)" {
			if cfg.IsSpeech {
				target = "-18"
				targetTp = "-1"
			} else {
				target = "-16"
				targetTp = "-1"
			}
		}
	*/
	// Staged processing at a single internal sample rate. The source is resampled
	// once, up front, to an integer multiple of the output rate (~350–384 kHz,
	// 32-bit float); every stage below runs at that rate with no further
	// resampling, and the only other rate change is the closing downsample to the
	// output rate at encode. Each stage still renders to its own temp file so the
	// next stage's analysis can read a finalized signal cheaply. Intermediate
	// codec is pcm_f32le because the bundled ffmpeg's whitelist excludes
	// pcm_f64le; ffmpeg's filtergraph runs at double precision internally
	// regardless of the on-disk format.
	workingPath = inputPath

	internalRate := internalSampleRate(outputRate)

	if !n.noTranscode {
		srcTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("tnt_src_%d.wav", time.Now().UnixNano()))
		tempFiles = append(tempFiles, srcTempPath)
		n.logFile.Write(fmt.Sprintf("Resampling to internal rate %d Hz (output %d Hz): %s", internalRate, outputRate, srcTempPath))
		n.appLog.Write(fmt.Sprintf("→ Resampling to %d Hz internal", internalRate))
		cmd := ffmpeg.Command(
			"-i", workingPath,
			"-ar", strconv.Itoa(internalRate),
			"-acodec", "pcm_f32le",
			"-y", srcTempPath,
		)
		if err := cmd.Run(); err != nil {
			n.appLog.Write(fmt.Sprintf("✗ Failed to resample to internal rate: %s", filepath.Base(inputPath)))
			n.logFile.Write(fmt.Sprintf("Internal resample failed: %v", err))
			return false
		}
		workingPath = srcTempPath
	}

	var eqFilter string
	var dynamicsFilter string
	var multibandFilter string

	n.logFile.Write(fmt.Sprintf("DEBUG: About to check EQ section - cfg.EqTarget='%s', cfg.EqTarget != ''=%v, cfg.EqTarget != 'Off'=%v, !cfg.BypassProc=%v",
		cfg.EqTarget,
		cfg.EqTarget != "",
		cfg.EqTarget != "Off",
		!cfg.BypassProc))

	// Stage 1: EQ analysis (measures the raw input spectrum). Every audio
	// stage is skipped in no-transcode (tag-only) mode: the final render is
	// -c copy, so a rendered intermediate could never reach the output — it
	// would only be wasted work and a broken stream-copy source.
	if cfg.EqTarget != "" && cfg.EqTarget != "Off" && !cfg.BypassProc && !n.noTranscode {
		eqBandAnalysis := n.analyzeFrequencyResponseBands(workingPath)
		if eqBandAnalysis == nil || len(eqBandAnalysis) == 0 {
			n.appLog.Write(fmt.Sprintf("✗ Failed to analyze frequency response: %s", filepath.Base(inputPath)))
			return false
		}

		n.logFile.Write(fmt.Sprintf("Frequency Response Analysis for %s:", filepath.Base(inputPath)))
		for _, band := range eqBandAnalysis {
			n.logFile.Write(fmt.Sprintf("  %s (%s): RMS=%.2f dB, Peak=%.2f dB, Crest=%.2f dB",
				band.Frequency, band.FilterType, band.RMSLevel, band.PeakLevel, band.CrestFactor))
		}

		eqFilter = n.buildEqFilter(eqBandAnalysis, cfg.EqTarget)
		n.logFile.Write(fmt.Sprintf("DEBUG: eqFilter value = '%s'", eqFilter))

		if eqFilter != "" {
			eqTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("tnt_eq_%d.wav", time.Now().UnixNano()))
			tempFiles = append(tempFiles, eqTempPath)
			n.logFile.Write(fmt.Sprintf("Added temp file: %s (total: %d)", eqTempPath, len(tempFiles)))

			n.appLog.Write(fmt.Sprintf("→ Applying EQ: %s", filepath.Base(inputPath)))

			fullEqFilter := eqFilter + ",deesser=i=1.0:m=1.0:f=0.05:s=o"
			cmd := ffmpeg.Command(
				"-i", workingPath,
				"-af", fullEqFilter,
				"-acodec", "pcm_f32le",
				"-y", eqTempPath,
			)

			n.logFile.Write(fmt.Sprintf("%s", cmd))

			if err := cmd.Run(); err != nil {
				n.appLog.Write(fmt.Sprintf("✗ Failed to apply EQ: %s", filepath.Base(inputPath)))
				n.logFile.Write(fmt.Sprintf("EQ application failed: %v", err))
				return false
			}

			workingPath = eqTempPath
			n.appLog.Write(fmt.Sprintf("✓ EQ applied: %s", filepath.Base(inputPath)))
		}
	}

	n.logFile.Write("")
	n.logFile.Write("")
	n.logFile.Write(fmt.Sprintf("args: %s", args))
	n.logFile.Write("")

	n.logFile.Write(fmt.Sprintf("DEBUG: About to check Dynamics section - cfg.DynamicsPreset='%s', cfg.DynamicsPreset != ''=%v, cfg.DynamicsPreset != 'Off'=%v, !cfg.BypassProc=%v",
		cfg.DynamicsPreset,
		cfg.DynamicsPreset != "",
		cfg.DynamicsPreset != "Off",
		!cfg.BypassProc))

	var dsAnalysis *audio.DynamicsScoreAnalysis
	if !cfg.BypassProc && !n.noTranscode && (cfg.DynamicsPreset != "" && cfg.DynamicsPreset != "Off") {
		dsAnalysis = n.calculateDynamicsScore(inputPath)
		if dsAnalysis == nil {
			n.appLog.Write(fmt.Sprintf("✗ Failed to calculate Dynamics Score: %s", filepath.Base(inputPath)))
			return false
		}
	}

	// Stage 2: Dynaudnorm (measures the post-EQ signal)
	if cfg.DynNorm && !cfg.BypassProc && !n.noTranscode {
		var dynaudnormFilter string
		dynamicsAnalysis := n.analyzeDynamics(workingPath)
		if dynamicsAnalysis == nil {
			n.appLog.Write(fmt.Sprintf("✗ Failed to analyze for dynaudnorm: %s", filepath.Base(inputPath)))
			return false
		}

		dynParams := n.analyzeDynaudnormParams(dynamicsAnalysis)
		if dynParams != nil {
			dynaudnormFilter = n.buildDynaudnormFilter(dynParams)

			if dynaudnormFilter != "" {
				dynTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("tnt_dyn_%d.wav", time.Now().UnixNano()))
				tempFiles = append(tempFiles, dynTempPath)
				n.logFile.Write(fmt.Sprintf("Added temp file: %s (total: %d)", dynTempPath, len(tempFiles)))

				n.appLog.Write(fmt.Sprintf("→ Applying dynamic normalization: %s", filepath.Base(inputPath)))
				cmd := ffmpeg.Command(
					"-i", workingPath,
					"-af", dynaudnormFilter,
					"-acodec", "pcm_f32le",
					"-y", dynTempPath,
				)

				if err := cmd.Run(); err != nil {
					n.appLog.Write(fmt.Sprintf("✗ Failed to apply dynaudnorm: %s", filepath.Base(inputPath)))
					n.logFile.Write(fmt.Sprintf("Dynaudnorm application failed: %v", err))
					return false
				}

				workingPath = dynTempPath
				n.appLog.Write(fmt.Sprintf("✓ Dynamic normalization applied: %s", filepath.Base(inputPath)))
			}
		}
	}

	// Stage 3: Compression (measures the post-EQ, post-dynaudnorm signal)
	if cfg.DynamicsPreset != "" && cfg.DynamicsPreset != "Off" && !cfg.BypassProc && !n.noTranscode {

		// MBC attenuates hot peaks before compressing
		var attenuatedPath string = workingPath
		if cfg.DynamicsPreset == "Broadcast" {
			// Quick peak check
			cmd := ffmpeg.Command("-i", workingPath, "-af", "astats", "-f", "null", "-")

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

					n.logFile.Write(fmt.Sprintf("Hot peaks detected (%.2f dBFS), creating attenuated temp: %.2f dB", peakLevel, inputAttenuationDb))

					cmd := ffmpeg.Command(
						"-i", workingPath,
						"-af", fmt.Sprintf("volume=%.6f", inputVolumeLinear),
						"-acodec", "pcm_f32le",
						"-y", attenuatedPath,
					)

					if err := cmd.Run(); err != nil {
						n.appLog.Write(fmt.Sprintf("✗ Failed to create attenuated temp: %s", filepath.Base(inputPath)))
						return false
					}
				}
			}
		}

		if cfg.DynamicsPreset == "Broadcast" {
			// MBC: analyze frequency bands of the (possibly attenuated) post-EQ signal
			bandAnalysis := n.analyzeFrequencyBands(attenuatedPath)
			if bandAnalysis == nil || len(bandAnalysis) == 0 {
				n.appLog.Write(fmt.Sprintf("✗ Failed to analyze frequency bands: %s", filepath.Base(inputPath)))
				return false
			}
			multibandFilter = n.buildMultibandCompression(bandAnalysis, dsAnalysis, cfg.DynamicsPreset)
		} else {
			// SBC: analyze dynamics of the post-EQ signal
			dynamicsAnalysis := n.analyzeDynamics(workingPath)
			if dynamicsAnalysis == nil {
				n.appLog.Write(fmt.Sprintf("✗ Failed to analyze dynamics: %s", filepath.Base(inputPath)))
				return false
			}

			n.logFile.Write(fmt.Sprintf("Dynamics Analysis for %s:", filepath.Base(inputPath)))
			n.logFile.Write(fmt.Sprintf("  Peak Level: %.2f dBFS", dynamicsAnalysis.PeakLevel))
			n.logFile.Write(fmt.Sprintf("  RMS Peak: %.2f dBFS", dynamicsAnalysis.RMSPeak))
			n.logFile.Write(fmt.Sprintf("  RMS Trough: %.2f dBFS", dynamicsAnalysis.RMSTrough))
			n.logFile.Write(fmt.Sprintf("  RMS Level: %.2f dBFS", dynamicsAnalysis.RMSLevel))
			n.logFile.Write(fmt.Sprintf("  Crest Factor: %.2f", dynamicsAnalysis.CrestFactor))

			dynamicsFilter = n.calculateAdaptiveCompression(dynamicsAnalysis, dsAnalysis, cfg.DynamicsPreset)
		}

		// Append whichever compression filter was built
		compressionFilter := multibandFilter
		if compressionFilter == "" {
			compressionFilter = dynamicsFilter
		}

		if compressionFilter != "" {
			compTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("tnt_comp_%d.wav", time.Now().UnixNano()))
			tempFiles = append(tempFiles, compTempPath)
			n.logFile.Write(fmt.Sprintf("Added temp file: %s (total: %d)", compTempPath, len(tempFiles)))

			n.appLog.Write(fmt.Sprintf("→ Applying compression: %s", filepath.Base(inputPath)))

			compressionInput := workingPath
			if cfg.DynamicsPreset == "Broadcast" && attenuatedPath != workingPath {
				compressionInput = attenuatedPath
			}

			cmd := ffmpeg.Command(
				"-i", compressionInput,
				"-af", compressionFilter,
				"-acodec", "pcm_f32le",
				"-y", compTempPath,
			)

			if err := cmd.Run(); err != nil {
				n.appLog.Write(fmt.Sprintf("✗ Failed to apply compression: %s", filepath.Base(inputPath)))
				n.logFile.Write(fmt.Sprintf("Compression application failed: %v", err))
				return false
			}

			workingPath = compTempPath
			n.appLog.Write(fmt.Sprintf("✓ Compression applied: %s", filepath.Base(inputPath)))
		}
	}

	n.logFile.Write("")
	n.logFile.Write(fmt.Sprintf("args: %s", args))
	n.logFile.Write("")

	// Stage 3.5: Speechnorm (speech content only). Rendered as its own
	// stage so the loudness measurement below reads the post-speechnorm
	// signal — otherwise loudnorm's linear=true gain is computed from
	// measurements taken upstream of the expansion and the final render
	// blows past target by speechnorm's gain.
	if cfg.UseLoudnorm && cfg.IsSpeech {
		spTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("tnt_sp_%d.wav", time.Now().UnixNano()))
		tempFiles = append(tempFiles, spTempPath)
		n.logFile.Write(fmt.Sprintf("Added temp file: %s (total: %d)", spTempPath, len(tempFiles)))

		n.appLog.Write(fmt.Sprintf("→ Applying speechnorm: %s", filepath.Base(inputPath)))
		cmd := ffmpeg.Command(
			"-i", workingPath,
			"-af", "speechnorm=e=12.5:r=0.0001:l=1",
			"-acodec", "pcm_f32le",
			"-y", spTempPath,
		)
		if err := cmd.Run(); err != nil {
			n.appLog.Write(fmt.Sprintf("✗ Failed to apply speechnorm: %s", filepath.Base(inputPath)))
			n.logFile.Write(fmt.Sprintf("Speechnorm application failed: %v", err))
			return false
		}

		workingPath = spTempPath
		n.appLog.Write(fmt.Sprintf("✓ Speechnorm applied: %s", filepath.Base(inputPath)))
	}

	// Loudness measurement that drives normalization is native — n.LUFS
	// (MeasureLUFS) in the loudness section below. The old ffmpeg loudnorm pass
	// that used to sit here was vestigial: its result fed only tag-writing and the
	// (commented-out) loudnorm filter chain, never the actual gain. Tags still get
	// their figures from ebur128 below when WriteTags is set.
	if cfg.WriteTags {
		measured = n.measureLoudnessEbuR128(workingPath)
		if measured == nil {
			n.appLog.Write(fmt.Sprintf("✗ Failed to measure: %s", filepath.Base(inputPath)))
			return false
		}
	}

	// strength for brightness reduction and the encoder pre-limiter. The
	// tiers are in kbps, but for the needsFullNumber codecs `bitrate` was
	// converted to full bps above — convert back before comparing.
	bitrateKbps := bitrate
	if needsFullNumber {
		bitrateKbps = bitrate / 1000
	}
	strength := brightnessStrength(actualCodec, bitrateKbps)

	n.logFile.Write("")
	n.logFile.Write(fmt.Sprintf("args: %s", args))
	n.logFile.Write("")

	// LOUDNESS: resolve the target. The user's custom values (n.normalizeTarget /
	// normalizeTargetTp) win whenever EITHER the standard radio is Custom OR the
	// "custom loudness targets" toggle is on (cfg.CustomLoudnorm). The toggle is
	// independent of the radio — a user can enable custom values while the radio
	// still shows a named standard — so both must be checked or the custom entry
	// is silently overridden by the standard's defaults. Empty fields fall back to
	// the EBU defaults. Otherwise the named standard supplies the target, with AES
	// applying the -2 LU speech attenuation via ITarget.
	target, targetTp = resolveLoudnessTarget(
		n.normalizationStandard, cfg.CustomLoudnorm,
		n.normalizeTarget, n.normalizeTargetTp,
		cfg.IsSpeech, target, targetTp,
	)

	// Native loudness chain — rewrites workingPath IN PLACE as 32-bit float
	// WAV. It runs even when cfg.BypassProc is set, on purpose: "Bypass all
	// processing" is scoped to the EQ/Dynamics stages above (GUI: "Disables
	// Dynamics and EQ regardless of selection above"); normalization is the
	// app's core function, not "processing". It must NOT run in no-transcode
	// (tag-only) mode: the resample stage was skipped there, so workingPath is
	// still the user's ORIGINAL input file — these calls would destroy it (or
	// fail outright on non-WAV input). Tag-only gets its ReplayGain figures
	// from the read-only ebur128 measurement above and never alters audio.
	if !n.noTranscode {
		// LRA reduction toward the target range (self-gates if already under target).
		const targetLRA = 7.0
		n.reduceLRA(workingPath, targetLRA, 4)

		// Normalize: measure the LRA-reduced signal and apply the linear offset to hit
		// the loudness target. True peak is the conformance limiter's job below, so the
		// full offset goes on here regardless of where it lands the peaks.
		n.logFile.Write(fmt.Sprintf("Measuring loudness for %s", workingPath))
		lufs, _, lra, err := n.LUFS(workingPath)
		if err != nil {
			n.logFile.Write(fmt.Sprintf("error: %v", err))
			return false
		}
		n.logFile.Write(fmt.Sprintf("lra: %.1f", lra))
		lOffset := target - lufs
		if err := n.Gain(workingPath, lOffset); err != nil {
			n.appLog.Write(fmt.Sprintf("✗ Failed to apply gain: %s", filepath.Base(inputPath)))
			n.logFile.Write(fmt.Sprintf("Gain application failed: %v", err))
			return false
		}

		// Encoder-specific character pre-limiter — lossy only. It pre-conditions the
		// peaks with the headroom each codec/bitrate needs (softLimiterCalibration via
		// strength) so the encoder's overshoot doesn't blow past the ceiling. PCM/FLAC
		// have no encoder to overshoot, so they skip it.
		if actualCodec != "PCM" && actualCodec != "flac" {
			if err := n.CharacterLimit(workingPath, strength); err != nil {
				n.logFile.Write(fmt.Sprintf("error in the encoder pre-limiter: %v", err))
				return false
			}
		}

		// Conformance limiter — true-peak, at the target TP ceiling. The
		// final guarantee the output stays under the ceiling.
		if err := n.conformLimit(workingPath, targetTp); err != nil {
			return false
		}
	}

	/*
		var loudnormFilterChain string
		if cfg.UseLoudnorm && measured != nil {
			loudnormFilterChain = fmt.Sprintf(
				"loudnorm=I=%s:TP=%s:LRA=5.0:measured_I=%s:measured_TP=%s:measured_LRA=%s:measured_thresh=%s:offset=%s:linear=true",
				target, targetTp,
				measured["input_i"], measured["input_tp"], measured["input_lra"], measured["input_thresh"], measured["target_offset"],
			)
		}
	*/

	n.logFile.Write("")
	n.logFile.Write(fmt.Sprintf("args: %s", args))
	n.logFile.Write("")

	// Final render reads workingPath (the last intermediate WAV, or the
	// original input if no stages ran) and applies loudnorm, optional
	// lossy-codec brightness reduction, and optional 16-bit dither — joined
	// into a single -af graph.
	var filterStages []string
	/*
		    if loudnormFilterChain != "" {
				filterStages = append(filterStages, loudnormFilterChain)
			}
	*/

	// No filter stages in no-transcode mode: -af cannot combine with -c copy.
	if _, isLossy := brightnessTiers[actualCodec]; isLossy && !n.noTranscode {
		if bf := n.buildBrightnessReduceFilterForLossy(strength); bf != "" {
			filterStages = append(filterStages, bf)
		}
	}

	// Add dithering for 16-bit PCM output.
	if actualCodec == "PCM" && cfg.BitDepth == "16" && !n.noTranscode {
		filterStages = append(filterStages, "aresample=resampler=soxr:dither_method=high_shibata")
	}

	finalFilterChain := strings.Join(filterStages, ",")

	args[1] = workingPath

	if finalFilterChain != "" {
		args = append(args, "-af", finalFilterChain)
	}

	var rgTpInLin float64

	if cfg.WriteTags {
		if measured["input_tp"] == "" {
			n.appLog.Write("ERROR: input_tp is empty")
			rgTpInLin = 1.0 // Default value
		} else {
			rgTpFlt, err := strconv.ParseFloat(measured["input_tp"], 64)
			if err != nil {
				n.appLog.Write("ERROR parsing peak: " + err.Error())
				rgTpInLin = 1.0 // Default on parse error
			} else {
				rgTpInLin = math.Pow(10, rgTpFlt/20)
				n.appLog.Write(fmt.Sprintf("Peak in linear: %.6f", rgTpInLin))
			}
		}
	}

	resultsInM4A := (actualCodec == "libfdk_aac" || actualCodec == "aac_at") || (cfg.OriginIsAAC && cfg.NoTranscode)
	useMovFlags := resultsInM4A && cfg.WriteTags && measured != nil

	if useMovFlags {
		args = append(args, "-movflags", "use_metadata_tags")
	}

	if cfg.WriteTags && measured != nil {
		inputI, _ := strconv.ParseFloat(measured["input_i"], 64)
		targetString := strconv.FormatFloat(target, 'f', -1, 64)
		gain := target - inputI

		args = append(args,
			"-metadata", fmt.Sprintf("REPLAYGAIN_TRACK_GAIN=%.2f dB", gain),
			"-metadata", fmt.Sprintf("REPLAYGAIN_TRACK_PEAK=%.6f", rgTpInLin),
			"-metadata", "REPLAYGAIN_REFERENCE_LOUDNESS="+targetString+" LUFS",
		)
	}

	// Coherence check reads workingPath as WAV — meaningless (and failing, for
	// non-WAV originals) in no-transcode mode where nothing was processed.
	if !n.noTranscode {
		rho, err := n.PCMFileCoherence(workingPath)
		if err != nil {
			print(fmt.Errorf("there was an error in coherence check: %v", err))
		}
		if rho > 0.4 {
			print(fmt.Sprintf("Channel coherence is probably fine, value: %.1f", rho))
		} else {
			print(fmt.Sprintf("Coherency might benefit from your attention: %.1f", rho))
		}
	}

	n.logFile.Write("")
	n.logFile.Write("")
	n.logFile.Write(fmt.Sprintf("DEBUG args: %#v", args))
	n.logFile.Write("")
	n.logFile.Write("")

	args = append(args, "-y", outputPath)

	fullCmdLog := ffmpegPath + " " + strings.Join(args, " ")
	n.logFile.Write(fullCmdLog)

	cmd := ffmpeg.Command(args...)

	// FINAL OUTPUT STEP
	output, err := ffmpeg.RunCmd(cmd)
	n.logFile.Write(fmt.Sprintf("FFmpeg output: %s", string(output)))

	if err != nil {
		n.appLog.Write(fmt.Sprintf("✗ Failed: %s - %v", filepath.Base(inputPath), err))
		n.logFile.Write(fmt.Sprintf("Failed %s - %v", filepath.Base(inputPath), err))
		n.logFile.Write(fmt.Sprintf("Error path - cleaning up %d temp files", len(tempFiles)))
		return false
	}

	if cfg.BitDepth != "" {
		n.logFile.Write(fmt.Sprintf("cfg.Bitdepth= %s", cfg.BitDepth))
	}

	if cfg.Bitrate != "" {
		n.logFile.Write(fmt.Sprintf("cfg.Bitrate= %s", cfg.Bitrate))
	}

	if cfg.SampleRate != "" {
		n.logFile.Write(fmt.Sprintf("cfg.SampleRate= %s", cfg.SampleRate))
	}

	if cfg.Format != "" {
		n.logFile.Write(fmt.Sprintf("cfg.Format= %s", cfg.Format))
	}

	if cfg.CustomLoudnorm {
		n.logFile.Write(fmt.Sprintf("Custom loudness values input and used:"))
		n.logFile.Write(fmt.Sprintf("LUFS I target: %.1f", target))
		n.logFile.Write(fmt.Sprintf("TP target: %.1f", targetTp))
	}

	if cfg.WriteTags && cfg.NoTranscode {
		n.logFile.Write("Writing tags and not transcoding")
		n.logFile.Write(fmt.Sprintf("Original format is: %s", originalExt))
		n.logFile.Write(fmt.Sprintf("LUFS I target: %.1f", target))
		n.logFile.Write(fmt.Sprintf("TP target: %.1f", targetTp))
	} else if cfg.WriteTags {
		n.logFile.Write(fmt.Sprintf("Writing tags and transcoding to %s", cfg.Format))
		n.logFile.Write(fmt.Sprintf("LUFS I target: %.1f", target))
		n.logFile.Write(fmt.Sprintf("TP target: %.1f", targetTp))
	}

	n.appLog.Write(fmt.Sprintf("✓ Success: %s", filepath.Base(inputPath)))
	n.logFile.Write(fmt.Sprintf("✓ Success: %s", filepath.Base(inputPath)))
	n.appLog.Write("")
	n.appLog.Write(fmt.Sprintf("Your files can be found from %s. Thank you.", n.outputDir))

	n.logFile.Write(fmt.Sprintf("Cleaning up %d temp files", len(tempFiles)))
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

	n.appLog.Write(result["input_i"])
	n.appLog.Write(result["input_lra"])
	n.appLog.Write(result["input_thresh"])
	n.appLog.Write(result["input_tp"])

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
		n.logFile.Write(fmt.Sprintf("measureLoudnessEbuR128 failed: %v\nOutput: %s", err, output))
		return nil
	}

	return n.parseEBUR128Output(string(output))
}

// logStatus moved to events.go (Phase 1 stub).

func isAudioFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	audioExts := []string{".mp3", ".wav", ".flac", ".m4a", ".aac", ".ogg", ".opus", ".wma", ".aiff", ".aif", ".ape"}

	acceptedExt := slices.Contains(audioExts, ext)
	if acceptedExt {
		return true
	}

	return false
}

func cleanupTempFiles(files []string) {
	for _, file := range files {
		if err := os.Remove(file); err != nil {
			// Log but don't fail - cleanup is best-effort
			fmt.Printf("Failed to remove temp file %s: %v\n", file, err)
		}
	}
}

// callerName returns the name of the function skip frames up the call stack.
func callerName(skip int) string {
	const unknown = "unknown"
	pcs := make([]uintptr, 1)
	n := runtime.Callers(skip+2, pcs)
	if n < 1 {
		return unknown
	}
	frame, _ := runtime.CallersFrames(pcs).Next()
	if frame.Function == "" {
		return unknown
	}
	return frame.Function
}

// timer returns a function that records the elapsed time since timer was
// called, written to the App Support log file (~/Library/Application
// Support/TNT/tnt.log via n.logFile) — NOT the in-app status log, which is
// fed separately by EventsEmit. Intended for use in a defer statement:
//
//	defer n.timer()()
func (n *AudioNormalizer) timer() func() {
	name := callerName(1)
	start := time.Now()
	return func() {
		n.logFile.Write(fmt.Sprintf("%s took %v", name, time.Since(start)))
	}
}
