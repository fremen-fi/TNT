package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/fremen-fi/tnt/go/internal/audio"
	"github.com/fremen-fi/tnt/go/internal/config"
	"github.com/fremen-fi/tnt/go/internal/ffmpeg"
)

// CLIConfig holds all CLI-parsed configuration
type CLIConfig struct {
	InputDir    string
	OutputDir   string
	Format      string
	SampleRate  string
	BitDepth    string
	Bitrate     string
	EqPreset    int // 0=off, 1=flat, 2=speech, 3=broadcast
	DynPreset   int // 0=off, 1=light, 2=moderate, 3=broadcast
	LufsEnabled bool
	LufsTargetI string
	LufsTargetTP string
	RGOnly      bool // ReplayGain tag only, no normalization
	DynNorm     bool
	Speech      bool
	NoTranscode bool
	DataComp    int
	PhaseCheck  bool
	Workers     int
}

// CLIProcessor handles CLI-mode processing without any GUI dependencies
type CLIProcessor struct {
	cfg     CLIConfig
	logFile *os.File
}

var cliMode bool

func parseCLIFlags() (*CLIConfig, bool) {
	// Check if any CLI flags are present
	if len(os.Args) < 2 {
		return nil, false
	}

	// If first arg doesn't start with '-', it's not CLI mode
	if !strings.HasPrefix(os.Args[1], "-") {
		return nil, false
	}

	cfg := &CLIConfig{}

	fs := flag.NewFlagSet("tnt", flag.ExitOnError)
	fs.StringVar(&cfg.InputDir, "i", "", "Input directory to watch (required)")
	fs.StringVar(&cfg.OutputDir, "o", "", "Output directory (required)")

	// Format flags
	format := fs.String("format", "pcm", "Output format: pcm, flac, opus, aac, mp3")
	fs.StringVar(&cfg.SampleRate, "sr", "48000", "Sample rate: 44100, 48000, 88200, 96000, 192000")
	fs.StringVar(&cfg.BitDepth, "bd", "24", "Bit depth: 16, 24, 32, 64 (32/64 are float)")
	fs.StringVar(&cfg.Bitrate, "br", "256", "Bitrate in kbps (for lossy codecs)")

	// Processing flags
	eqPreset := fs.Int("p:eq", 0, "EQ preset: 0=off, 1=flat, 2=speech, 3=broadcast")
	dynPreset := fs.Int("p:dyn", 0, "Dynamics preset: 0=off, 1=light, 2=moderate, 3=broadcast")

	// LUFS normalization
	lufs := fs.Int("lufs", 1, "Enable LUFS normalization: 1=on, 0=off")
	fs.StringVar(&cfg.LufsTargetI, "lufs-target-i", "-23", "LUFS integrated loudness target (e.g. -23)")
	fs.StringVar(&cfg.LufsTargetTP, "lufs-target-tp", "-1", "LUFS true peak target (e.g. -1)")

	// ReplayGain
	rg := fs.Int("rg", 0, "ReplayGain tagging: 1=tag only (no normalize), 0=off")

	// Other
	dynNorm := fs.Int("dyn-norm", 0, "Dynamic normalization (dynaudnorm): 1=on, 0=off")
	speech := fs.Int("speech", 0, "Opus speech optimization: 1=on, 0=off")
	noTranscode := fs.Int("no-transcode", 0, "Copy codec, no transcode: 1=on, 0=off")
	dataComp := fs.Int("comp", 0, "Data compression level 0-10 (FLAC/Opus)")
	phaseCheck := fs.Int("phase-check", 0, "Phase check before processing: 1=on, 0=off")
	workers := fs.Int("workers", 0, "Number of worker threads (0=auto: CPU cores - 1)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "TNT %s - CLI Daemon Mode\n", currentVersion)
		fmt.Fprintf(os.Stderr, "Watches a directory and processes audio files.\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  tnt -i /dir/to/watch -o /dir/to/out [options]\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  tnt -i ./inbox -o ./processed -p:eq 1 -p:dyn 2 -lufs 1 -lufs-target-i -23 -lufs-target-tp -1\n")
		fmt.Fprintf(os.Stderr, "  tnt -i ./inbox -o ./tagged -rg 1 -format flac\n")
		fmt.Fprintf(os.Stderr, "  tnt -i ./inbox -o ./out -format mp3 -br 320 -p:dyn 3 -lufs 1\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, false
	}

	// Validate required flags
	if cfg.InputDir == "" || cfg.OutputDir == "" {
		fs.Usage()
		os.Exit(1)
	}

	// Map format string
	switch strings.ToLower(*format) {
	case "pcm", "wav":
		cfg.Format = "PCM"
	case "flac":
		cfg.Format = "FLAC"
	case "opus":
		cfg.Format = "Opus"
	case "aac", "m4a":
		cfg.Format = "AAC"
	case "mp3":
		cfg.Format = "MPEG-II L3"
	default:
		fmt.Fprintf(os.Stderr, "Unknown format: %s\n", *format)
		os.Exit(1)
	}

	cfg.EqPreset = *eqPreset
	cfg.DynPreset = *dynPreset
	cfg.LufsEnabled = *lufs == 1
	cfg.RGOnly = *rg == 1
	cfg.DynNorm = *dynNorm == 1
	cfg.Speech = *speech == 1
	cfg.NoTranscode = *noTranscode == 1
	cfg.DataComp = *dataComp
	cfg.PhaseCheck = *phaseCheck == 1
	cfg.Workers = *workers

	// Map bit depth for display
	switch cfg.BitDepth {
	case "32":
		cfg.BitDepth = "32 (float)"
	case "64":
		cfg.BitDepth = "64 (float)"
	}

	// Ensure targets have negative sign
	if cfg.LufsTargetI != "" && !strings.HasPrefix(cfg.LufsTargetI, "-") {
		cfg.LufsTargetI = "-" + cfg.LufsTargetI
	}
	if cfg.LufsTargetTP != "" && !strings.HasPrefix(cfg.LufsTargetTP, "-") {
		cfg.LufsTargetTP = "-" + cfg.LufsTargetTP
	}

	return cfg, true
}

func (c *CLIConfig) eqPresetName() string {
	switch c.EqPreset {
	case 1:
		return "Flat"
	case 2:
		return "Speech"
	case 3:
		return "Broadcast"
	default:
		return "Off"
	}
}

func (c *CLIConfig) dynPresetName() string {
	switch c.DynPreset {
	case 1:
		return "Light"
	case 2:
		return "Moderate"
	case 3:
		return "Broadcast"
	default:
		return "Off"
	}
}

func runCLI(cfg *CLIConfig) {
	// Validate directories
	if info, err := os.Stat(cfg.InputDir); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: input directory does not exist: %s\n", cfg.InputDir)
		os.Exit(1)
	}

	if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot create output directory: %s\n", cfg.OutputDir)
		os.Exit(1)
	}

	// Resolve absolute paths
	cfg.InputDir, _ = filepath.Abs(cfg.InputDir)
	cfg.OutputDir, _ = filepath.Abs(cfg.OutputDir)

	proc := &CLIProcessor{cfg: *cfg}
	proc.logFile = proc.initLogFile()
	if proc.logFile != nil {
		defer proc.logFile.Close()
	}

	proc.log("TNT CLI daemon starting")
	proc.log(fmt.Sprintf("Input:  %s", cfg.InputDir))
	proc.log(fmt.Sprintf("Output: %s", cfg.OutputDir))
	proc.log(fmt.Sprintf("Format: %s, SR: %s, BD: %s, BR: %s kbps", cfg.Format, cfg.SampleRate, cfg.BitDepth, cfg.Bitrate))
	proc.log(fmt.Sprintf("EQ: %s, Dynamics: %s, DynNorm: %v", cfg.eqPresetName(), cfg.dynPresetName(), cfg.DynNorm))
	proc.log(fmt.Sprintf("LUFS: %v (I=%s, TP=%s), RG-only: %v", cfg.LufsEnabled, cfg.LufsTargetI, cfg.LufsTargetTP, cfg.RGOnly))

	fmt.Printf("TNT %s - CLI daemon mode\n", currentVersion)
	fmt.Printf("Watching: %s\n", cfg.InputDir)
	fmt.Printf("Output:   %s\n", cfg.OutputDir)
	fmt.Printf("Format: %s | EQ: %s | Dynamics: %s | LUFS: %v | RG: %v\n",
		cfg.Format, cfg.eqPresetName(), cfg.dynPresetName(), cfg.LufsEnabled, cfg.RGOnly)
	fmt.Println("Press Ctrl+C to stop.")

	// Process existing files first
	proc.processExistingFiles()

	// Start watching
	proc.watchAndProcess()
}

func (p *CLIProcessor) initLogFile() *os.File {
	configDir, _ := os.UserConfigDir()
	logDir := filepath.Join(configDir, "TNT")
	os.MkdirAll(logDir, 0755)

	logPath := filepath.Join(logDir, "tnt-cli.log")

	if data, err := os.ReadFile(logPath); err == nil {
		lines := strings.Count(string(data), "\n")
		if lines > 1000 {
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

func (p *CLIProcessor) log(message string) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	formatted := fmt.Sprintf("[%s] %s", timestamp, message)
	fmt.Println(formatted)
	if p.logFile != nil {
		p.logFile.WriteString(formatted + "\n")
	}
}

func (p *CLIProcessor) logQuiet(message string) {
	if p.logFile != nil {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		p.logFile.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, message))
	}
}

func (p *CLIProcessor) processExistingFiles() {
	entries, err := os.ReadDir(p.cfg.InputDir)
	if err != nil {
		return
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && isAudioFile(e.Name()) {
			files = append(files, filepath.Join(p.cfg.InputDir, e.Name()))
		}
	}

	if len(files) == 0 {
		return
	}

	p.log(fmt.Sprintf("Found %d existing files to process", len(files)))
	p.processFiles(files)
}

func (p *CLIProcessor) processFiles(files []string) {
	workers := p.cfg.Workers
	if workers <= 0 {
		workers = max(1, runtime.NumCPU()-1)
	}

	jobs := make(chan string, len(files))
	results := make(chan bool, len(files))
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range jobs {
				if p.cfg.PhaseCheck {
					inverted, offset, err := audio.PhaseCheck(file, p.logFile)
					if err != nil {
						p.log(fmt.Sprintf("Phase check failed for %s: %v", filepath.Base(file), err))
					} else if inverted {
						p.log(fmt.Sprintf("WARNING: Phase inverted (offset: %.6f): %s - skipping", offset, filepath.Base(file)))
						results <- false
						continue
					}
				}
				success := p.processFile(file)
				results <- success
			}
		}()
	}

	for _, file := range files {
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
	}

	p.log(fmt.Sprintf("Batch complete: %d/%d files processed successfully", successful, processed))
}

func (p *CLIProcessor) watchAndProcess() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		p.log(fmt.Sprintf("Failed to create watcher: %v", err))
		os.Exit(1)
	}
	defer watcher.Close()

	if err := watcher.Add(p.cfg.InputDir); err != nil {
		p.log(fmt.Sprintf("Failed to watch directory: %v", err))
		os.Exit(1)
	}

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	jobQueue := make(chan string, 100)

	// Worker pool for watch mode
	workers := p.cfg.Workers
	if workers <= 0 {
		workers = max(1, runtime.NumCPU()-1)
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range jobQueue {
				// Small delay to let file writes complete
				time.Sleep(500 * time.Millisecond)

				if p.cfg.PhaseCheck {
					inverted, offset, err := audio.PhaseCheck(file, p.logFile)
					if err != nil {
						p.log(fmt.Sprintf("Phase check failed for %s: %v", filepath.Base(file), err))
					} else if inverted {
						p.log(fmt.Sprintf("WARNING: Phase inverted (offset: %.6f): %s - skipping", offset, filepath.Base(file)))
						continue
					}
				}
				p.processFile(file)
			}
		}()
	}

	p.log("Watching for new files...")

	for {
		select {
		case event := <-watcher.Events:
			if event.Op&fsnotify.Create == fsnotify.Create && isAudioFile(event.Name) {
				p.log(fmt.Sprintf("New file detected: %s", filepath.Base(event.Name)))
				jobQueue <- event.Name
			}
		case err := <-watcher.Errors:
			p.log(fmt.Sprintf("Watcher error: %v", err))
		case <-sigChan:
			p.log("Shutting down...")
			close(jobQueue)
			wg.Wait()
			p.log("Goodbye.")
			return
		}
	}
}

// processFile processes a single audio file in CLI mode
// This replicates the logic from AudioNormalizer.processFile but without GUI dependencies
func (p *CLIProcessor) processFile(inputPath string) bool {
	cfg := p.cfg
	p.log(fmt.Sprintf("Processing: %s", filepath.Base(inputPath)))

	actualCodec := cfg.Format
	var workingPath string = inputPath
	var tempFiles []string
	defer func() { cleanupTempFiles(tempFiles) }()

	if platformCodec := getPlatformCodecMap()[cfg.Format]; platformCodec != "" {
		actualCodec = platformCodec
	} else if codec := config.GetCodec(cfg.Format); codec != "" {
		actualCodec = codec
	}

	baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))

	// Determine output extension
	var ext string
	switch actualCodec {
	case "libopus":
		ext = ".opus"
	case "libfdk_aac", "aac", "aac_at":
		ext = ".m4a"
	case "libmp3lame":
		ext = ".mp3"
	case "PCM":
		ext = ".wav"
	case "flac":
		ext = ".flac"
	default:
		ext = filepath.Ext(inputPath)
	}

	originalExt := filepath.Ext(inputPath)
	originIsAAC := strings.ToLower(strings.TrimPrefix(originalExt, ".")) == "m4a"

	// Determine naming
	var outputPath string
	useLufs := cfg.LufsEnabled && !cfg.RGOnly
	writeTags := cfg.RGOnly

	if useLufs {
		outputPath = filepath.Join(cfg.OutputDir, fmt.Sprintf("%s.normalized%s", baseName, ext))
	} else if writeTags && cfg.NoTranscode {
		outputPath = filepath.Join(cfg.OutputDir, fmt.Sprintf("%s.tagged%s", baseName, originalExt))
	} else if writeTags {
		outputPath = filepath.Join(cfg.OutputDir, fmt.Sprintf("%s.tagged%s", baseName, ext))
	} else {
		outputPath = filepath.Join(cfg.OutputDir, fmt.Sprintf("%s%s", baseName, ext))
	}

	p.logQuiet(fmt.Sprintf("Config: EqTarget='%s', DynamicsPreset='%s', Format=%s, Codec=%s",
		cfg.eqPresetName(), cfg.dynPresetName(), cfg.Format, actualCodec))

	// Build ffmpeg output args
	args := []string{"-i", workingPath, "-vn"}

	if cfg.NoTranscode {
		args = append(args, "-c", "copy")
	} else if actualCodec == "PCM" {
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
		default:
			codec = "pcm_s24le"
		}
		args = append(args, "-acodec", codec)
	} else {
		args = append(args, "-ar", "48000")
		args = append(args, "-c:a", actualCodec)
	}

	// Bitrate handling
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
	if err != nil || bitrate <= 12 {
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

	// Speech optimization for Opus
	if cfg.Speech && actualCodec == "libopus" && !cfg.NoTranscode {
		args = append(args, "-application", "voip")
	} else if !cfg.Speech && actualCodec == "libopus" && !cfg.NoTranscode {
		args = append(args, "-application", "audio")
	}

	// Data compression level
	usesDataCompression := actualCodec == "flac" || actualCodec == "libopus"
	if usesDataCompression {
		var level int
		if actualCodec == "libopus" {
			level = 10 - cfg.DataComp
		} else if actualCodec == "flac" {
			level = int(math.Round(float64(cfg.DataComp) * 12.0 / 10.0))
		}
		args = append(args, "-compression_level", fmt.Sprintf("%d", level))
	}

	// Targets
	target := cfg.LufsTargetI
	targetTp := cfg.LufsTargetTP

	// === STAGED PROCESSING ===

	eqTarget := cfg.eqPresetName()
	dynPreset := cfg.dynPresetName()

	// Stage 1: EQ
	if eqTarget != "Off" {
		eqBandAnalysis := p.analyzeFrequencyResponseBands(workingPath)
		if eqBandAnalysis == nil || len(eqBandAnalysis) == 0 {
			p.log(fmt.Sprintf("Failed to analyze frequency response: %s", filepath.Base(inputPath)))
			return false
		}

		eqFilter := p.buildEqFilter(eqBandAnalysis, eqTarget)
		if eqFilter != "" {
			eqTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("tnt_eq_%d.wav", time.Now().UnixNano()))
			tempFiles = append(tempFiles, eqTempPath)

			p.log(fmt.Sprintf("  Applying EQ (%s): %s", eqTarget, filepath.Base(inputPath)))

			fullEqFilter := eqFilter + ",deesser=i=1.0:m=1.0:f=0.05:s=o"
			cmd := ffmpeg.Command(
				"-i", workingPath,
				"-af", fullEqFilter,
				"-ar", "192000",
				"-acodec", "pcm_f64le",
				"-y", eqTempPath,
			)

			if err := cmd.Run(); err != nil {
				p.log(fmt.Sprintf("  Failed to apply EQ: %s - %v", filepath.Base(inputPath), err))
				return false
			}

			workingPath = eqTempPath
			p.log(fmt.Sprintf("  EQ applied: %s", filepath.Base(inputPath)))
		}
	}

	// Dynamics Score (needed for compression stages)
	var dsAnalysis *audio.DynamicsScoreAnalysis
	if dynPreset != "Off" {
		dsAnalysis = p.calculateDynamicsScore(inputPath)
		if dsAnalysis == nil {
			p.log(fmt.Sprintf("  Failed to calculate Dynamics Score: %s", filepath.Base(inputPath)))
			return false
		}
	}

	// Stage 2: Dynamic normalization (dynaudnorm)
	var measured map[string]string
	if cfg.DynNorm {
		dynamicsAnalysis := p.analyzeDynamics(workingPath)
		if dynamicsAnalysis == nil {
			p.log(fmt.Sprintf("  Failed to analyze for dynaudnorm: %s", filepath.Base(inputPath)))
			return false
		}

		dynParams := p.analyzeDynaudnormParams(dynamicsAnalysis)
		if dynParams != nil {
			dynaudnormFilter := p.buildDynaudnormFilter(dynParams)
			if dynaudnormFilter != "" {
				dynTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("tnt_dyn_%d.wav", time.Now().UnixNano()))
				tempFiles = append(tempFiles, dynTempPath)

				p.log(fmt.Sprintf("  Applying dynaudnorm: %s", filepath.Base(inputPath)))
				cmd := ffmpeg.Command(
					"-i", workingPath,
					"-af", dynaudnormFilter,
					"-ar", "192000",
					"-acodec", "pcm_f64le",
					"-y", dynTempPath,
				)

				if err := cmd.Run(); err != nil {
					p.log(fmt.Sprintf("  Failed to apply dynaudnorm: %s - %v", filepath.Base(inputPath), err))
					return false
				}

				workingPath = dynTempPath

				if useLufs {
					measured = p.measureLoudness(workingPath, target, targetTp)
				}
				if writeTags {
					measured = p.measureLoudnessEbuR128(workingPath)
				}
			}
		}
	}

	// Stage 3: Dynamics / Compression
	if dynPreset != "Off" {
		var attenuatedPath string = workingPath

		if dynPreset == "Broadcast" {
			// Quick peak check for hot peaks
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

					p.logQuiet(fmt.Sprintf("Hot peaks (%.2f dBFS), attenuating %.2f dB", peakLevel, inputAttenuationDb))

					cmd := ffmpeg.Command(
						"-i", workingPath,
						"-af", fmt.Sprintf("volume=%.6f", inputVolumeLinear),
						"-ar", "192000",
						"-acodec", "pcm_f64le",
						"-y", attenuatedPath,
					)
					if err := cmd.Run(); err != nil {
						p.log(fmt.Sprintf("  Failed to attenuate: %s", filepath.Base(inputPath)))
						return false
					}
				}
			}
		}

		var compressionFilter string

		if dynPreset == "Broadcast" {
			bandAnalysis := p.analyzeFrequencyBands(attenuatedPath)
			if bandAnalysis == nil || len(bandAnalysis) == 0 {
				p.log(fmt.Sprintf("  Failed to analyze frequency bands: %s", filepath.Base(inputPath)))
				return false
			}
			compressionFilter = p.buildMultibandCompression(bandAnalysis, dsAnalysis, dynPreset)
		} else {
			dynamicsAnalysis := p.analyzeDynamics(workingPath)
			if dynamicsAnalysis == nil {
				p.log(fmt.Sprintf("  Failed to analyze dynamics: %s", filepath.Base(inputPath)))
				return false
			}
			compressionFilter = p.calculateAdaptiveCompression(dynamicsAnalysis, dsAnalysis, dynPreset)
		}

		if compressionFilter != "" {
			compTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("tnt_comp_%d.wav", time.Now().UnixNano()))
			tempFiles = append(tempFiles, compTempPath)

			p.log(fmt.Sprintf("  Applying %s compression: %s", dynPreset, filepath.Base(inputPath)))

			compressionInput := workingPath
			if dynPreset == "Broadcast" && attenuatedPath != workingPath {
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
				p.log(fmt.Sprintf("  Failed to apply compression: %s - %v", filepath.Base(inputPath), err))
				return false
			}

			workingPath = compTempPath
		}
	}

	// Stage 4: Measure loudness
	if useLufs {
		measured = p.measureLoudness(workingPath, target, targetTp)
		if measured == nil {
			p.log(fmt.Sprintf("  Failed to measure loudness: %s", filepath.Base(inputPath)))
			return false
		}
	}

	if writeTags {
		measured = p.measureLoudnessEbuR128(workingPath)
		if measured == nil {
			p.log(fmt.Sprintf("  Failed to measure EBU R128: %s", filepath.Base(inputPath)))
			return false
		}
	}

	// Build final loudnorm filter chain
	var loudnormFilterChain string
	if useLufs && measured != nil {
		if cfg.Speech {
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

	// Build final filter chain
	var finalFilterChain string
	if loudnormFilterChain != "" {
		finalFilterChain = loudnormFilterChain
	}

	args[1] = workingPath

	// Dithering for 16-bit PCM
	if actualCodec == "PCM" && cfg.BitDepth == "16" {
		if finalFilterChain != "" {
			finalFilterChain += ",aresample=resampler=soxr:dither_method=triangular"
		} else {
			finalFilterChain = "aresample=resampler=soxr:dither_method=triangular"
		}
	}

	if finalFilterChain != "" {
		args = append(args, "-af", finalFilterChain)
	}

	// ReplayGain tags
	var rgTpInLin float64
	if writeTags && measured != nil {
		if measured["input_tp"] != "" {
			rgTpFlt, err := strconv.ParseFloat(measured["input_tp"], 64)
			if err == nil {
				rgTpInLin = math.Pow(10, rgTpFlt/20)
			} else {
				rgTpInLin = 1.0
			}
		} else {
			rgTpInLin = 1.0
		}
	}

	resultsInM4A := (actualCodec == "libfdk_aac" || actualCodec == "aac") || (originIsAAC && cfg.NoTranscode)
	if resultsInM4A && writeTags && measured != nil {
		args = append(args, "-movflags", "use_metadata_tags")
	}

	if writeTags && measured != nil {
		inputI, _ := strconv.ParseFloat(measured["input_i"], 64)
		targetFloat, _ := strconv.ParseFloat(target, 64)
		gain := targetFloat - inputI

		args = append(args,
			"-metadata", fmt.Sprintf("REPLAYGAIN_TRACK_GAIN=%.2f dB", gain),
			"-metadata", fmt.Sprintf("REPLAYGAIN_TRACK_PEAK=%.6f", rgTpInLin),
			"-metadata", "REPLAYGAIN_REFERENCE_LOUDNESS="+target+" LUFS",
		)
	}

	args = append(args, "-y", outputPath)

	p.logQuiet(fmt.Sprintf("Final command: ffmpeg %s", strings.Join(args, " ")))

	cmd := ffmpeg.Command(args...)
	output, err := cmd.CombinedOutput()
	p.logQuiet(fmt.Sprintf("FFmpeg output: %s", string(output)))

	if err != nil {
		p.log(fmt.Sprintf("  FAILED: %s - %v", filepath.Base(inputPath), err))
		return false
	}

	p.log(fmt.Sprintf("  OK: %s -> %s", filepath.Base(inputPath), filepath.Base(outputPath)))
	return true
}

// ============================================================
// Analysis and filter-building methods for CLIProcessor
// These replicate the AudioNormalizer methods without GUI deps
// ============================================================

func (p *CLIProcessor) analyzeDynamics(inputPath string) *DynamicsAnalysis {
	cmd := ffmpeg.Command("-i", inputPath, "-af", "astats=metadata=1:length=0.05", "-f", "null", "-")
	output, err := cmd.CombinedOutput()
	if err != nil {
		p.logQuiet(fmt.Sprintf("astats failed: %v", err))
		return nil
	}
	return p.parseAstatsOutput(string(output))
}

func (p *CLIProcessor) parseAstatsOutput(output string) *DynamicsAnalysis {
	result := &DynamicsAnalysis{}

	overallStart := strings.Index(output, "Overall")
	if overallStart == -1 {
		return result
	}
	overallSection := output[overallStart:]

	peakRe := regexp.MustCompile(`Peak level dB:\s+([-\d.]+)`)
	if match := peakRe.FindStringSubmatch(overallSection); len(match) > 1 {
		result.PeakLevel, _ = strconv.ParseFloat(match[1], 64)
	}

	rmsPeakRe := regexp.MustCompile(`RMS peak dB:\s+([-\d.]+)`)
	if match := rmsPeakRe.FindStringSubmatch(overallSection); len(match) > 1 {
		result.RMSPeak, _ = strconv.ParseFloat(match[1], 64)
	}

	rmsTroughRe := regexp.MustCompile(`RMS trough dB:\s+([-\d.]+)`)
	if match := rmsTroughRe.FindStringSubmatch(overallSection); len(match) > 1 {
		result.RMSTrough, _ = strconv.ParseFloat(match[1], 64)
	}

	rmsRe := regexp.MustCompile(`RMS level dB:\s+([-\d.]+)`)
	if match := rmsRe.FindStringSubmatch(overallSection); len(match) > 1 {
		result.RMSLevel, _ = strconv.ParseFloat(match[1], 64)
	}

	crestRe := regexp.MustCompile(`Crest factor:\s+([-\d.]+)`)
	if match := crestRe.FindStringSubmatch(output); len(match) > 1 {
		result.CrestFactor, _ = strconv.ParseFloat(match[1], 64)
	}

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

func (p *CLIProcessor) analyzeFrequencyBands(inputPath string) map[string]*FrequencyBandAnalysis {
	bands := map[string]string{
		"sub":     "lowpass=f=80",
		"bass":    "highpass=f=80,lowpass=f=250",
		"low_mid": "highpass=f=250,lowpass=f=1000",
		"mid":     "highpass=f=1000,lowpass=f=4000",
		"high":    "highpass=f=4000",
	}

	results := make(map[string]*FrequencyBandAnalysis)

	for bandName, filter := range bands {
		cmd := exec.Command(ffmpegPath, "-i", inputPath, "-af", fmt.Sprintf("%s,astats", filter), "-f", "null", "-")
		output, err := cmd.CombinedOutput()
		if err != nil {
			continue
		}

		analysis := p.parseFrequencyBandOutput(string(output), bandName)
		if analysis != nil {
			results[bandName] = analysis
		}
	}

	return results
}

func (p *CLIProcessor) parseFrequencyBandOutput(output string, bandName string) *FrequencyBandAnalysis {
	result := &FrequencyBandAnalysis{BandName: bandName}

	overallStart := strings.Index(output, "Overall")
	if overallStart == -1 {
		return nil
	}
	overallSection := output[overallStart:]

	peakRe := regexp.MustCompile(`Peak level dB:\s+([-\d.]+)`)
	if match := peakRe.FindStringSubmatch(overallSection); len(match) > 1 {
		result.PeakLevel, _ = strconv.ParseFloat(match[1], 64)
	}

	rmsRe := regexp.MustCompile(`RMS level dB:\s+([-\d.]+)`)
	if match := rmsRe.FindStringSubmatch(overallSection); len(match) > 1 {
		result.RMSLevel, _ = strconv.ParseFloat(match[1], 64)
	}

	crestRe := regexp.MustCompile(`Crest factor:\s+([-\d.]+)`)
	if match := crestRe.FindStringSubmatch(output); len(match) > 1 {
		result.CrestFactor, _ = strconv.ParseFloat(match[1], 64)
	}

	dynRe := regexp.MustCompile(`Dynamic range:\s+([-\d.]+)`)
	if match := dynRe.FindStringSubmatch(output); len(match) > 1 {
		result.DynamicRange, _ = strconv.ParseFloat(match[1], 64)
	}

	return result
}

func (p *CLIProcessor) buildMultibandCompression(bandAnalysis map[string]*FrequencyBandAnalysis, dsAnalysis *audio.DynamicsScoreAnalysis, preset string) string {
	if len(bandAnalysis) == 0 || preset == "Off" {
		return ""
	}

	var mods audio.CompressionModifiers
	if dsAnalysis != nil {
		mods = audio.GetCompressionModifiers(dsAnalysis.DynamicsScore)
	} else {
		mods = audio.CompressionModifiers{AttackMultiplier: 1.0, ReleaseMultiplier: 1.0, RatioMultiplier: 1.0}
	}

	sub := bandAnalysis["sub"]
	bass := bandAnalysis["bass"]
	lowMid := bandAnalysis["low_mid"]
	mid := bandAnalysis["mid"]
	high := bandAnalysis["high"]

	var attackMs, releaseMs, baseRatio float64
	switch preset {
	case "Light":
		attackMs, releaseMs, baseRatio = 150, 300, 2.5
	case "Moderate":
		attackMs, releaseMs, baseRatio = 100, 200, 4.0
	case "Broadcast":
		attackMs, releaseMs, baseRatio = 10, 20, 6.0
	}

	subFilter := p.buildBandAcompressor(sub, attackMs, releaseMs, baseRatio, -18, mods)
	bassFilter := p.buildBandAcompressor(bass, attackMs, releaseMs, baseRatio, -15, mods)
	lowMidFilter := p.buildBandAcompressor(lowMid, attackMs*0.8, releaseMs*0.9, baseRatio*1.2, -12, mods)
	midFilter := p.buildBandAcompressor(mid, attackMs*0.6, releaseMs*0.7, baseRatio*1.5, -10, mods)
	highFilter := p.buildBandAcompressor(high, attackMs*0.5, releaseMs*0.6, baseRatio*2.0, -8, mods)

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

func (p *CLIProcessor) buildBandAcompressor(band *FrequencyBandAnalysis, attackMs float64, releaseMs float64, ratio float64, fallbackThresholdDb float64, mods audio.CompressionModifiers) string {
	if band == nil {
		thresholdLin := math.Pow(10, fallbackThresholdDb/20)
		makeup := math.Pow(10, 3.0/20)
		limiterLin := math.Pow(10, -1.0/20)
		if limiterLin > 1.0 {
			limiterLin = 1.0
		}
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

	thresholdLin := math.Pow(10, adaptiveThresholdDb/20)

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
	makeupLin := math.Pow(10, makeupGainDb/20)

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

	limiterLin := math.Pow(10, limiterCeilingDb/20)
	if limiterLin > 1.0 {
		limiterLin = 1.0
	}

	attackMs *= mods.AttackMultiplier
	releaseMs *= mods.ReleaseMultiplier
	ratio *= mods.RatioMultiplier

	limiterAttack := 25.0 * mods.AttackMultiplier
	limiterRelease := 150.0 * mods.ReleaseMultiplier

	knee := 4.0
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

	// Clamp values
	thresholdLin = max(0.00099, min(1.0, thresholdLin))
	attackMs = max(0.01, min(2000.0, attackMs))
	releaseMs = max(0.01, min(9000.0, releaseMs))
	makeupLin = max(1.0, min(64.0, makeupLin))
	limiterAttack = min(80.0, limiterAttack)
	limiterRelease = min(8000.0, limiterRelease)

	if mods.RatioMultiplier < 0.3 {
		limiterCeilingDb = 0.0
		limiterAttack = 80.0
		limiterRelease = 2000.0
	}

	return fmt.Sprintf("acompressor=threshold=%.6f:ratio=%.1f:attack=%.1f:release=%.1f:makeup=1.0:knee=%1.f,alimiter=limit=%.6f:attack=%.0f:release=%.0f:level=false,volume=%.3f",
		thresholdLin, ratio, attackMs, releaseMs, knee, limiterLin, limiterAttack, limiterRelease, makeupLin)
}

func (p *CLIProcessor) calculateAdaptiveCompression(analysis *DynamicsAnalysis, dsAnalysis *audio.DynamicsScoreAnalysis, preset string) string {
	if analysis == nil || preset == "Off" {
		return ""
	}

	var threshold, ratio, attack, release float64
	var limiterCeiling float64
	needsLimiting := analysis.CrestFactor > 5.0

	switch preset {
	case "Light":
		threshold = analysis.RMSLevel + 6.0
		ratio = audio.GetBaseRatioFromCrest(analysis.CrestFactor)
		attack, release = 100, 250
		limiterCeiling = -1.0
	case "Moderate":
		threshold = analysis.RMSLevel + 5.0
		ratio = audio.GetBaseRatioFromCrest(analysis.CrestFactor)
		attack, release = 40, 150
		limiterCeiling = -1.0
	case "Broadcast":
		threshold = analysis.RMSLevel + 4.0
		ratio = audio.GetBaseRatioFromCrest(analysis.CrestFactor)
		attack, release = 10, 30
		limiterCeiling = -1.0
	}

	if dsAnalysis != nil {
		mods := audio.GetCompressionModifiers(dsAnalysis.DynamicsScore)
		attack *= mods.AttackMultiplier
		release *= mods.ReleaseMultiplier
		ratio *= mods.RatioMultiplier
	}

	makeupGain := calculateMakeupGain(analysis, threshold, ratio)
	thresholdLin := math.Pow(10, threshold/20)

	knee := 4.0
	thresholdLin = max(0.00099, min(1.0, thresholdLin))

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

	ratio = min(20.0, ratio)
	attack = max(0.01, min(2000.0, attack))
	release = max(0.01, min(9000.0, release))
	makeupGain = max(1.0, min(64.0, makeupGain))

	filterChain := fmt.Sprintf(
		"acompressor=threshold=%.6f:ratio=%.1f:attack=%.0f:release=%.0f:knee=%.1f:makeup=%.1f",
		thresholdLin, ratio, attack, release, knee, makeupGain,
	)

	if needsLimiting {
		limiterLinear := math.Pow(10, limiterCeiling/20)
		if limiterLinear > 1.0 {
			limiterLinear = 1.0
		}
		filterChain += fmt.Sprintf(",alimiter=limit=%.6f:attack=5:release=50", limiterLinear)
	}

	return filterChain
}

func (p *CLIProcessor) calculateDynamicsScore(inputPath string) *audio.DynamicsScoreAnalysis {
	cmd := ffmpeg.Command("-i", inputPath, "-af", "astats", "-f", "null", "-")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}

	result := &audio.DynamicsScoreAnalysis{}
	lines := strings.Split(string(output), "\n")
	inChannel1 := false

	for _, line := range lines {
		if strings.Contains(line, "Channel: 1") {
			inChannel1 = true
			continue
		}
		if strings.Contains(line, "Channel: 2") {
			break
		}
		if inChannel1 {
			if strings.Contains(line, "RMS peak dB:") {
				re := regexp.MustCompile(`RMS peak dB:\s+([-\d.]+)`)
				if match := re.FindStringSubmatch(line); len(match) > 1 {
					result.RMSPeak, _ = strconv.ParseFloat(match[1], 64)
				}
			}
			if strings.Contains(line, "RMS level dB:") && result.RMSLevel == 0 {
				re := regexp.MustCompile(`RMS level dB:\s+([-\d.]+)`)
				if match := re.FindStringSubmatch(line); len(match) > 1 {
					result.RMSLevel, _ = strconv.ParseFloat(match[1], 64)
				}
			}
			if strings.Contains(line, "Crest factor:") {
				re := regexp.MustCompile(`Crest factor:\s+([-\d.]+)`)
				if match := re.FindStringSubmatch(line); len(match) > 1 {
					result.CrestFactor, _ = strconv.ParseFloat(match[1], 64)
				}
			}
		}
	}

	result.DynamicsScore = math.Sqrt(result.CrestFactor) * (result.RMSPeak - result.RMSLevel)
	p.logQuiet(fmt.Sprintf("DS: %.2f (RMS Peak: %.2f, RMS Level: %.2f, Crest: %.2f)",
		result.DynamicsScore, result.RMSPeak, result.RMSLevel, result.CrestFactor))

	return result
}

func (p *CLIProcessor) analyzeDynaudnormParams(analysis *DynamicsAnalysis) *DynaudnormParams {
	if analysis == nil {
		return nil
	}

	params := &DynaudnormParams{
		RMSPeakDB:    analysis.RMSPeak,
		NoiseFloorDB: analysis.NoiseFloor,
	}

	targetDB := params.RMSPeakDB - 6.0
	params.TargetRMS = math.Pow(10, targetDB/20)

	thresholdDB := params.NoiseFloorDB + 12.0
	params.Threshold = math.Pow(10, thresholdDB/20)

	params.TargetRMS = max(0.0, min(1.0, params.TargetRMS))
	params.Threshold = max(0.0, min(1.0, params.Threshold))

	return params
}

func (p *CLIProcessor) buildDynaudnormFilter(params *DynaudnormParams) string {
	if params == nil {
		return ""
	}
	return fmt.Sprintf(
		"dynaudnorm=framelen=650:gausssize=36:targetrms=%.6f:threshold=%.6f:altboundary=true:overlap=0.95",
		params.TargetRMS, params.Threshold,
	)
}

func (p *CLIProcessor) measureLoudness(inputPath string, target string, targetTp string) map[string]string {
	p.log(fmt.Sprintf("  Measuring loudness: %s", filepath.Base(inputPath)))

	cmd := exec.Command(ffmpegPath, "-i", inputPath,
		"-af", fmt.Sprintf("loudnorm=linear=false:I=%s:TP=%s:LRA=5:print_format=json", target, targetTp),
		"-f", "null", "-")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}

	return p.parseLoudnormJSON(string(output))
}

func (p *CLIProcessor) measureLoudnessEbuR128(inputPath string) map[string]string {
	cmd := exec.Command(ffmpegPath, "-i", inputPath,
		"-af", "ebur128=framelog=quiet:peak=true",
		"-f", "null", "-")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}

	return p.parseEBUR128Output(string(output))
}

func (p *CLIProcessor) parseLoudnormJSON(output string) map[string]string {
	re := regexp.MustCompile(`(?s)\{[^\}]*"input_i"[^\}]*\}`)
	jsonMatch := re.FindString(output)
	if jsonMatch == "" {
		return nil
	}

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

func (p *CLIProcessor) parseEBUR128Output(output string) map[string]string {
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

// Frequency response analysis (mirrors AudioNormalizer methods)

func (p *CLIProcessor) analyzeFrequencyResponseBands(inputPath string) []FrequencyBand {
	bands := []FrequencyBand{
		{Frequency: "50Hz", FilterType: "lowpass"},
		{Frequency: "100Hz", FilterType: "bandpass"},
		{Frequency: "200Hz", FilterType: "bandpass"},
		{Frequency: "400Hz", FilterType: "bandpass"},
		{Frequency: "800Hz", FilterType: "bandpass"},
		{Frequency: "1.6kHz", FilterType: "bandpass"},
		{Frequency: "3.2kHz", FilterType: "bandpass"},
		{Frequency: "6.4kHz", FilterType: "bandpass"},
		{Frequency: "12.8kHz", FilterType: "bandpass"},
		{Frequency: "12.8kHz+", FilterType: "highpass"},
	}

	for i := range bands {
		band := &bands[i]

		var filterChain string
		switch band.FilterType {
		case "lowpass":
			filterChain = "highpass=f=25:p=1:r=f64:p=2,lowpass=f=50,astats"
		case "highpass":
			filterChain = "highpass=f=12800,astats"
		case "bandpass":
			centerFreq, _ := getBandpassParamsCLI(band.Frequency)
			filterChain = fmt.Sprintf("bandpass=f=%d:width_type=o:width=1,astats", centerFreq)
		}

		cmd := ffmpeg.Command("-i", inputPath, "-af", filterChain, "-f", "null", "-")
		output, err := cmd.CombinedOutput()
		if err != nil {
			continue
		}

		stats := parseFrequencyBandStatsCLI(string(output))
		band.RMSLevel = stats["rms"]
		band.PeakLevel = stats["peak"]
		band.CrestFactor = stats["crest"]
	}

	return bands
}

func getBandpassParamsCLI(freqStr string) (int, float64) {
	freqMap := map[string]int{
		"100Hz": 100, "200Hz": 200, "400Hz": 400, "800Hz": 800,
		"1.6kHz": 1600, "3.2kHz": 3200, "6.4kHz": 6400, "12.8kHz": 12800,
	}
	centerFreq := freqMap[freqStr]
	return centerFreq, float64(centerFreq)
}

func parseFrequencyBandStatsCLI(output string) map[string]float64 {
	stats := make(map[string]float64)

	rmsRe := regexp.MustCompile(`RMS level dB:\s+([-\d.]+)`)
	if match := rmsRe.FindStringSubmatch(output); len(match) > 1 {
		if val, err := strconv.ParseFloat(match[1], 64); err == nil {
			stats["rms"] = val
		}
	}

	peakRe := regexp.MustCompile(`Peak level dB:\s+([-\d.]+)`)
	if match := peakRe.FindStringSubmatch(output); len(match) > 1 {
		if val, err := strconv.ParseFloat(match[1], 64); err == nil {
			stats["peak"] = val
		}
	}

	crestRe := regexp.MustCompile(`Crest factor:\s+([-\d.]+)`)
	if match := crestRe.FindStringSubmatch(output); len(match) > 1 {
		if val, err := strconv.ParseFloat(match[1], 64); err == nil {
			stats["crest"] = val
		}
	}

	return stats
}

func (p *CLIProcessor) buildEqFilter(bands []FrequencyBand, eqTarget string) string {
	if len(bands) == 0 || eqTarget == "Off" {
		return ""
	}

	var highpassFilter, lowpassFilter string
	switch eqTarget {
	case "Flat":
		highpassFilter = "highpass=f=25:p=2"
	case "Speech":
		highpassFilter = "highpass=f=80:p=2"
		lowpassFilter = "lowpass=f=13000:p=1"
	case "Broadcast":
		highpassFilter = "highpass=f=70:p=2"
		lowpassFilter = "lowpass=f=14000:p=2"
	}

	targetLevels := p.calculateTargetCurve(bands, eqTarget)

	gains := make([]float64, len(bands))
	for i, band := range bands {
		gain := targetLevels[i] - band.RMSLevel
		const maxGain = 10.0
		gains[i] = max(-maxGain, min(maxGain, gain))
	}

	gains = clampExtremeEQCLI(gains, p)

	var filterParts []string
	for i, band := range bands {
		gain := gains[i]
		if gain > -0.5 && gain < 0.5 {
			continue
		}

		switch band.FilterType {
		case "lowpass":
			filterParts = append(filterParts, fmt.Sprintf("highpass=f=25:p=1:r=f64:p=2,lowshelf=f=50:g=%.2f:width_type=q:width=0.7", gain))
		case "highpass":
			filterParts = append(filterParts, fmt.Sprintf("lowpass=f=17500:p=2:r=f64,highshelf=f=12800:g=%.2f:width_type=q:width=0.7", gain))
		case "bandpass":
			centerFreq, bandwidth := getBandpassParamsCLI(band.Frequency)
			filterParts = append(filterParts, fmt.Sprintf("anequalizer=c0 f=%d w=%.0f g=%.2f t=0|c1 f=%d w=%.0f g=%.2f t=0",
				centerFreq, bandwidth, gain, centerFreq, bandwidth, gain))
		}
	}

	var finalParts []string
	if highpassFilter != "" {
		finalParts = append(finalParts, highpassFilter)
	}
	finalParts = append(finalParts, filterParts...)
	if lowpassFilter != "" {
		finalParts = append(finalParts, lowpassFilter)
	}

	if len(finalParts) == 0 {
		return ""
	}

	return strings.Join(finalParts, ",")
}

func (p *CLIProcessor) calculateTargetCurve(bands []FrequencyBand, eqTarget string) []float64 {
	targets := make([]float64, len(bands))

	var overallRMS float64
	for _, band := range bands {
		overallRMS += band.RMSLevel
	}
	overallRMS /= float64(len(bands))

	switch eqTarget {
	case "Flat":
		for i, band := range bands {
			pinkNoiseRef := overallRMS
			if band.RMSLevel > pinkNoiseRef {
				excess := band.RMSLevel - pinkNoiseRef
				attenuation := min(10.0, excess/2.0)
				targets[i] = band.RMSLevel - attenuation
			} else {
				targets[i] = band.RMSLevel
			}
		}

	case "Speech":
		adjustments := map[string]float64{
			"50Hz": -9.0, "100Hz": -3.5, "200Hz": -2.5, "400Hz": -3.0, "800Hz": +0.5,
			"1.6kHz": +3.0, "3.2kHz": +1.0, "6.4kHz": +0.0, "12.8kHz": -2.0, "12.8kHz+": -2.0,
		}
		for i, band := range bands {
			octaves := getOctavesFrom1kCLI(band.Frequency)
			pinkNoiseRef := overallRMS - (octaves * 3.0)
			adj := adjustments[band.Frequency]
			targetLevel := pinkNoiseRef + adj
			deviation := band.RMSLevel - targetLevel
			correction := max(-10.0, min(10.0, deviation/2.0))
			if correction > -0.5 && correction < 0.5 {
				targets[i] = band.RMSLevel
			} else {
				targets[i] = band.RMSLevel - correction
			}
		}

	case "Broadcast":
		adjustments := map[string]float64{
			"50Hz": -2.0, "100Hz": -1.0, "200Hz": -2.5, "400Hz": -4.5, "800Hz": +1.0,
			"1.6kHz": +2.5, "3.2kHz": +3.5, "6.4kHz": +2.0, "12.8kHz": -0.5, "12.8kHz+": -2.5,
		}
		for i, band := range bands {
			octaves := getOctavesFrom1kCLI(band.Frequency)
			pinkNoiseRef := overallRMS - (octaves * 3.0)
			adj := adjustments[band.Frequency]
			targetLevel := pinkNoiseRef + adj
			deviation := band.RMSLevel - targetLevel
			correction := max(-10.0, min(10.0, deviation/2.0))
			if correction > -0.5 && correction < 0.5 {
				targets[i] = band.RMSLevel
			} else {
				targets[i] = band.RMSLevel - correction
			}
		}

	default:
		for i, band := range bands {
			targets[i] = band.RMSLevel
		}
	}

	return targets
}

func getOctavesFrom1kCLI(freqStr string) float64 {
	m := map[string]float64{
		"50Hz": -4.32, "100Hz": -3.32, "200Hz": -2.32, "400Hz": -1.32, "800Hz": -0.32,
		"1.6kHz": 0.68, "3.2kHz": 1.68, "6.4kHz": 2.68, "12.8kHz": 3.68, "12.8kHz+": 5.0,
	}
	return m[freqStr]
}

func clampExtremeEQCLI(gains []float64, p *CLIProcessor) []float64 {
	if len(gains) < 3 {
		return gains
	}

	clamped := make([]float64, len(gains))
	copy(clamped, gains)

	lowShelf := gains[0]
	highShelf := gains[len(gains)-1]

	var sum float64
	for i := 1; i < len(gains)-1; i++ {
		sum += gains[i]
	}
	avgNonExtremes := sum / float64(len(gains)-2)

	firstNeighbor := gains[1]
	lastNeighbor := gains[len(gains)-2]

	// Clamp low shelf
	maxLowFromHigh := highShelf + 4.0
	minLowFromHigh := highShelf - 4.0
	maxLowFromNeighbor := firstNeighbor + 4.0
	minLowFromNeighbor := firstNeighbor - 4.0

	var maxLowFromAvg, minLowFromAvg float64
	if avgNonExtremes >= 0 {
		maxLowFromAvg = avgNonExtremes
		minLowFromAvg = -999.0
	} else {
		maxLowFromAvg = 999.0
		minLowFromAvg = avgNonExtremes
	}

	maxLow := math.Min(math.Min(maxLowFromHigh, maxLowFromNeighbor), maxLowFromAvg)
	minLow := math.Max(math.Max(minLowFromHigh, minLowFromNeighbor), minLowFromAvg)

	if lowShelf > maxLow {
		clamped[0] = maxLow
	} else if lowShelf < minLow {
		clamped[0] = minLow
	}

	// Clamp high shelf
	maxHighFromLow := clamped[0] + 4.0
	minHighFromLow := clamped[0] - 4.0
	maxHighFromNeighbor := lastNeighbor + 4.0
	minHighFromNeighbor := lastNeighbor - 4.0

	var maxHighFromAvg, minHighFromAvg float64
	if avgNonExtremes >= 0 {
		maxHighFromAvg = avgNonExtremes
		minHighFromAvg = -999.0
	} else {
		maxHighFromAvg = 999.0
		minHighFromAvg = avgNonExtremes
	}

	maxHigh := math.Min(math.Min(maxHighFromLow, maxHighFromNeighbor), maxHighFromAvg)
	minHigh := math.Max(math.Max(minHighFromLow, minHighFromNeighbor), minHighFromAvg)

	if highShelf > maxHigh {
		clamped[len(clamped)-1] = maxHigh
	} else if highShelf < minHigh {
		clamped[len(clamped)-1] = minHigh
	}

	return clamped
}
