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

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type VersionInfo struct {
	Version      string `json:"version"`
	DownloadURL  string `json:"download_url"`
	ReleaseNotes string `json:"release_notes"`
}

func checkForUpdates(currentVersion string, window fyne.Window) {
	resp, err := http.Get("https://software.collins.fi/tnt-version.json")
	if err != nil {
		return // Silently fail if can't reach server
	}
	defer resp.Body.Close()
	
	var versionInfo VersionInfo
	if err := json.NewDecoder(resp.Body).Decode(&versionInfo); err != nil {
		return
	}
	
	if compareVersions(versionInfo.Version, currentVersion) > 0 {
		dialog.ShowConfirm(
			"Update Available",
			fmt.Sprintf("Version %s is available!\n\n%s", versionInfo.Version, versionInfo.ReleaseNotes),
			func(download bool) {
				if download {
					exec.Command("open", versionInfo.DownloadURL).Start()
				}
			},
			window,
		)
	}
}

func compareVersions(v1, v2 string) int {
	return strings.Compare(v1, v2)
}

func extractFFmpeg() string {
	// Extract to temp location
	tmpDir := os.TempDir()
	ffmpegPath := filepath.Join(tmpDir, "ffmpeg")
	
	os.WriteFile(ffmpegPath, ffmpegBinary, 0755)
	return ffmpegPath
}

var ffmpegPath string

func init() {
	ffmpegPath = extractFFmpeg()
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
	
	// Mode toggle
	advancedMode bool
	modeToggle   *widget.Check
	
	// Simple mode
	simpleGroup *widget.RadioGroup
	
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
	IsSpeechCheck *widget.Check
	writeTags *widget.Check
	noTranscode *widget.Check
	
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
}

var codecMap = map[string]string{
	"Opus": "libopus",
	"AAC": "libfdk_aac",
	"MPEG-II L3": "libmp3lame",
	"PCM": "PCM",
	"Small file (AAC 256kbps)": "libfdk_aac",
	"Most compatible (MP3 160kbps)": "libmp3lame",
	"Production (PCM 48kHz/24bit)": "PCM",
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
	
	norm.setupUI()
	
	const currentVersion = "1.0.1"
	go checkForUpdates(currentVersion, w)
	
	w.ShowAndRun()
}

func (n *AudioNormalizer) setupUI() {
	n.fileList = widget.NewList(
		func() int { return len(n.files) },
		func() fyne.CanvasObject {
			return widget.NewLabel("template")
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(filepath.Base(n.files[i]))
		},
	)
	
	// Mode toggle
	n.modeToggle = widget.NewCheck("Advanced Mode", func(checked bool) {
		n.advancedMode = checked
		n.updateModeUI()
	})
	
	// Simple mode widgets
	n.simpleGroup = widget.NewRadioGroup([]string{
		"Small file (AAC 256kbps)",
		"Most compatible (MP3 160kbps)",
		"Production (PCM 48kHz/24bit)",
	}, nil)
	n.simpleGroup.SetSelected("Production (PCM 48kHz/24bit)")
	
	// Advanced mode widgets
	n.sampleRate = widget.NewSelect([]string{"44100", "48000", "88200", "96000", "192000"}, nil)
	n.sampleRate.SetSelected("48000")
	
	n.bitDepth = widget.NewSelect([]string{"16", "24", "32 (float)", "64 (float)"}, nil)
	n.bitDepth.SetSelected("24")
	
	n.bitrateEntry = widget.NewEntry()
	n.bitrateEntry.SetPlaceHolder("Bitrate (kbps)")
	n.bitrateEntry.SetText("256")
	
	n.normalizeTarget = widget.NewEntry()
	n.normalizeTarget.SetPlaceHolder("LUFS target")
	n.normalizeTarget.SetText("-23")
	
	n.normalizeTargetTp = widget.NewEntry()
	n.normalizeTargetTp.SetPlaceHolder("TP limit")
	n.normalizeTargetTp.SetText("-1")
	
	n.writeTags = widget.NewCheck("Write ReplayGain tags", func(checked bool) {
		if checked  && n.checkPCM(){
			n.loudnormCheck.Disable()
			n.noTranscode.Disable()
			n.noTranscode.SetChecked(false)
		} else if checked {
			n.loudnormCheck.Disable()
			n.loudnormCheck.SetChecked(false)
			n.noTranscode.Enable()
		} else {
			n.loudnormCheck.Enable()
		}
	})
	n.writeTags.SetChecked(false)
	n.writeTags.Disable()
	
	n.noTranscode = widget.NewCheck("Do not transcode", nil) 
	n.noTranscode.SetChecked(false)
	n.noTranscode.Disable()
	
	n.loudnormCustomCheck = widget.NewCheck("Custom loudness", func(checked bool) {
		if n.loudnormCustomCheck.Checked {
			n.normalizeTarget.Enable()
			n.normalizeTargetTp.Enable()
		} else {
			n.normalizeTarget.Disable()
			n.normalizeTargetTp.Disable()
			n.normalizeTarget.SetText("-23")
			n.normalizeTargetTp.SetText("-1")
		}
	})
	n.loudnormCustomCheck.SetChecked(false)
	n.normalizeTarget.Disable()
	n.normalizeTargetTp.Disable()
	
	formatLabel := widget.NewLabel("Format:")
	sampleRateLabel := widget.NewLabel("Sample Rate:")
	bitDepthLabel := widget.NewLabel("Bit Depth:")
	bitrateLabel := widget.NewLabel("Bitrate (kbps):")
	normalizeTargetLabel := widget.NewLabel("Target in LUFS")
	normalizeTpLabel := widget.NewLabel("TP limit in dB")

	n.advancedContainer = container.NewVBox(
		container.NewBorder(nil, nil, formatLabel, nil, widget.NewLabel("")),
		container.NewBorder(nil, nil, sampleRateLabel, nil, n.sampleRate),
		container.NewBorder(nil, nil, bitDepthLabel, nil, n.bitDepth),
		container.NewBorder(nil, nil, bitrateLabel, nil, n.bitrateEntry),
		container.NewBorder(nil, nil, normalizeTargetLabel, nil, n.normalizeTarget),
		container.NewBorder(nil, nil, normalizeTpLabel, nil, n.normalizeTargetTp),
		n.loudnormCustomCheck,
		n.writeTags,
		n.noTranscode,
	)
	
	n.IsSpeechCheck = widget.NewCheck("The content is speech, use Opus", func(checked bool){
		if checked {
				n.formatSelect.SetSelected("Opus")
				n.formatSelect.Disable()
		} else {
			n.formatSelect.Enable()
			n.formatSelect.SetSelected("AAC")
		}
	})
	n.IsSpeechCheck.SetChecked(false)
	
	// Create format select after container exists
	n.formatSelect = widget.NewSelect([]string{"Opus", "AAC", "MPEG-II L3", "PCM"}, func(value string) {
		n.updateAdvancedControls()
	})
	n.formatSelect.SetSelected("AAC")
	
	// Replace placeholder with actual format select
	n.advancedContainer.Objects[0] = container.NewBorder(nil, nil, formatLabel, nil, n.formatSelect)
	
	// Loudnorm checkbox
	n.loudnormCheck = widget.NewCheck("Normalize (EBU R128: -23 LUFS)", func(checked bool) {
		if checked {
			n.writeTags.Disable()
		} else {
			n.writeTags.Enable()
		}
	})
	n.loudnormCheck.SetChecked(false)
	
	// File selection
	selectFilesBtn := widget.NewButton("Select Files", n.selectFiles)
	selectFolderBtn := widget.NewButton("Select Folder", n.selectFolder)
	
	n.outputLabel = widget.NewLabel("No output folder selected")
	selectOutputBtn := widget.NewButton("Output Folder", n.selectOutputFolder)
	
	n.processBtn = widget.NewButton("Process", n.process)
	n.processBtn.Disable()
	
	n.progressBar = widget.NewProgressBar()
	n.progressBar.Hide()
	
	n.statusLog = widget.NewMultiLineEntry()
	n.statusLog.Disable()
	n.statusLog.SetPlaceHolder("Processing log will appear here...")
		
	helpBtn := widget.NewButton("Help", func() {
			
			menuGettingStarted := widget.NewLabel(				
`This tool is designed for broadcast houses to make the workflows as efficient as possible. This tool allows you to transcode (change formats), normalize (make sure the loudness is just right) and tag (give instructions to players as to what volume should the file be played at). 

The tool has two modes. The Simple mode allows you to change format to one of the three pre-selected formats. It also allows you to normalize the audio file to EBU R128. Processing one or more files requires four clicks, and the program can be left running in background. Ready files will appear to the selected output folder once they're ready (one by one).

Advanced mode lets you choose what encoding format to use and what encoding values should be used. It also lets you set custom targets for loudness normalization, and it lets you choose whether you want to normalize or set ReplayGain tags. (there's no point in normalizing AND tagging)

'Select Files' selects files to process.

'Select Folder' selects folder full of files to process.

'Output Folder' chooses directory to output the processed audio files.

For more information visit collinsgroup.fi/en/software/tnt`)
			menuGettingStarted.Wrapping = fyne.TextWrapWord
			
			menuSimpleTab := widget.NewLabel(`
Choose the end format, minimal options.

Checkbox 'Normalize' allows you to normalize the audio file to EBU R128 standard.`)
			menuSimpleTab.Wrapping = fyne.TextWrapWord
			
			menuAdvancedTab := widget.NewLabel(
`Choose format out of AAC, Opus, MP3 and PCM (Wave).

Sample rate and bit depth are disabled for all codecs except for PCM.

Birate is available for codecs other than PCM. Minimum is 12 kbps and maximum is set by the encoder.

Target in LUFS and TP limit in dB are for either custom normalization or ReplayGain tagging. To use these, check 'Use custom loudness...'

Checkbox 'Custom loudness' allows you to configure custom target for LUFS I and TP. Values will always be parsed into negative values. Uncheck to use EBU R128 (if Normalize is selected).

Checkbox 'Write RG tags' writes ReplayGain tags to the audio file metadata. It uses custom values, if above is checked, or EBU R128 if unchecked. This can not be checked with 'Normalize' or a PCM origin file.

Checkbox 'Do not transcode' is an option for ReplayGain tagging. It does not alter the audio data, only writes metadata. This can only be used with the above checked, and if the original file is not PCM.

Checkbox 'Normalize' normalizes the audio files to custom values, if relevant checkbox is checked, or to EBU R128 if no custom values are given. It uses BS.1770-5.

Checkbox 'Speech' sets the codec to Opus and chooses data compression suitable for VoIP applications. If used with normalize, it uses normalization suited better for speech. Do not use with music.`)
			menuAdvancedTab.Wrapping = fyne.TextWrapWord
			
			menuFormatsTab := widget.NewLabel(
`AAC is a data compression method that at high bitrates can sound similar to a non-compressed file. In simple mode, the bitrate is set to 256 kbit/s, which gives very good results. The maximum bitrate for this encoder is 512 kbit/s. At 320 kbit/s the encoder tends to lose almost all of its encoding artifacts. Thirty seconds of audio encoded with 256 kbit/s results in approximately 1 MB filesize.

Opus is a modern data compression method that can achieve very good results even with lower bitrates. Opus has a lower algorithmic delay, which makes it suitable for live applications. It's an open-source format. It's minimum bitrate is 6 kbit/s, though the UI limits the bitrate at 12 kbit/s at minimum. The maximum bitrate for this encoder is 510 kbit/s.

MPEG-II Layer 3 (AKA mp3) is an older, but one of the most compatible encoder available. It isn't as capable at lower bitrates as the two encoders above, but at high bitrates (>320 kbit/s) it's usable. Use this if you know the end-user can't decode AAC or Opus. Filesize for mp3 at 160 kbit/s for 30 second audio file is 0.6 MB.

PCM, or WAV in this tool is a pulse-code modulated, raw uncompressed audio stream. It's the highest quality, but it comes with a size-cost. This encoder doesn't have a bitrate setting, but has two other settings that result in a bitrate. First, samplerate (either 44.1, 48, 88.2, 96, 192 kHz) mean "how often the original data is converted into audio in a second". With 48 kHz the audio is sampled forty-eight thousand times in a second. Second, the bitrate controls "how precisely we want to have each sample". The options are either 16, 24, 32 or 64, of which the last two are floating-point and used in specific scenarions. The file size for a thirty-second audio with 48 kHz, 24-bit audio is 8.64 MB.`)
			menuFormatsTab.Wrapping = fyne.TextWrapWord
			
			tabs := container.NewAppTabs(
				container.NewTabItem("Getting started", container.NewScroll(menuGettingStarted)),
				container.NewTabItem("Simple", container.NewScroll(menuSimpleTab)),
				container.NewTabItem("Advanced", container.NewScroll(menuAdvancedTab)),
				container.NewTabItem("Audio formats", container.NewScroll(menuFormatsTab)),			)
			
			tabs.SetTabLocation(container.TabLocationTop)
			
		helpWindow := fyne.CurrentApp().NewWindow("Help")
		helpWindow.SetContent(tabs)
		helpWindow.Resize(fyne.NewSize(600, 400))
		helpWindow.Show()
	})
	
	
	
	// Layout
	settingsContainer := container.NewVBox(
		helpBtn,
		n.modeToggle,
		widget.NewSeparator(),
		n.simpleGroup,
		n.advancedContainer,
		n.loudnormCheck,
	)
	
	topButtons := container.NewHBox(selectFilesBtn, selectFolderBtn)
	outputSection := container.NewBorder(nil, nil, widget.NewLabel("Output:"), selectOutputBtn, n.outputLabel)
	
	content := container.NewBorder(
		container.NewVBox(
			settingsContainer,
			widget.NewSeparator(),
			topButtons,
			outputSection,
			widget.NewSeparator(),
		),
		container.NewVBox(
			n.progressBar,
			container.NewHBox(n.processBtn),
		),
		nil,
		nil,
		container.NewBorder(
			widget.NewLabel("Files to process:"),
			nil,
			nil,
			nil,
			n.fileList,
		),
	)
	
	split := container.NewVSplit(content, n.statusLog)
	split.SetOffset(0.6)
	
	n.window.SetContent(split)
	n.updateModeUI()
}

func (n *AudioNormalizer) updateModeUI() {
	if n.advancedMode {
		n.simpleGroup.Hide()
		n.advancedContainer.Show()
		n.updateAdvancedControls()
	} else {
		n.advancedContainer.Hide()
		n.simpleGroup.Show()
	}
	n.window.Content().Refresh()
}

func (n *AudioNormalizer) updateAdvancedControls() {
	isPCM := n.formatSelect.Selected == "PCM"
	
	if n.IsSpeechCheck.Checked {
		if n.formatSelect.Selected != "libopus" && n.formatSelect.Selected != "PCM" {
			n.formatSelect.SetSelected("libopus")
		}
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
}

func (n *AudioNormalizer) selectFolder() {
	dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil || uri == nil {
			return
		}
		
		n.logStatus("Scanning folder...")
		
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
				for _, existing := range n.files {
					if existing == file {
						exists = true
						break
					}
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

func (n *AudioNormalizer) updateProcessButton() {
	if len(n.files) > 0 && n.outputDir != "" {
		n.processBtn.Enable()
	} else {
		n.processBtn.Disable()
	}
}

func (n *AudioNormalizer) getProcessConfig() ProcessConfig {
	config := ProcessConfig{
		UseLoudnorm: n.loudnormCheck.Checked,
		IsSpeech: n.IsSpeechCheck.Checked,
	}
	
	if n.advancedMode {
		config.Format = n.formatSelect.Selected
		config.SampleRate = n.sampleRate.Selected
		config.BitDepth = n.bitDepth.Selected
		config.Bitrate = n.bitrateEntry.Text
		config.writeTags = n.writeTags.Checked
	} else {
		switch n.simpleGroup.Selected {
		case "Small file (AAC 256kbps)":
			config.Format = "libfdk_aac"
			config.Bitrate = "256000"
		case "Most compatible (MP3 160kbps)":
			config.Format = "libmp3lame"
			config.Bitrate = "160"
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
					success := n.processFile(file, config)
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

func (n *AudioNormalizer) processFile(inputPath string, config ProcessConfig) bool {
	actualCodec := config.Format
	if codecMap[config.Format] != "" {
		actualCodec = codecMap[config.Format]
	}	
	
	baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	
	// Determine output extension
	var ext string
	switch actualCodec {
	case "libopus":
		ext = ".opus"
	case "libfdk_aac":
		ext = ".m4a"
	case "libmp3lame":
		ext = ".mp3"
	case "PCM":
		ext = ".wav"
	default:
		ext = filepath.Ext(inputPath)
	}
	
	outputPath := filepath.Join(n.outputDir, fmt.Sprintf("%s.normalized%s", baseName, ext))

	if n.noTranscode.Checked {
		outputPath = filepath.Join(n.outputDir, fmt.Sprintf("%s.tagged%s", baseName, ext))
	}
	
	n.logStatus(fmt.Sprintf("Processing: %s, outputting to %s", filepath.Base(inputPath), outputPath))
	
	var measured map[string]string
	
	if config.writeTags {
		// Use accurate ebur128 for tagging
		measured = n.measureLoudnessEbuR128(inputPath)
		if measured == nil {
			n.logStatus(fmt.Sprintf("✗ Failed to measure: %s", filepath.Base(inputPath)))
			return false
		}
	} else if config.UseLoudnorm {
		// Use loudnorm for normalization measurement
		measured = n.measureLoudness(inputPath)
		if measured == nil {
			n.logStatus(fmt.Sprintf("✗ Failed to measure: %s", filepath.Base(inputPath)))
			return false
		}
	}
	
	// Build ffmpeg command
	args := []string{"-i", inputPath}
	
	// Add format-specific arguments
	if n.noTranscode.Checked {
		args = append(args, "-c", "copy")
	} else if actualCodec == "PCM" && !n.noTranscode.Checked {
		args = append(args, "-ar", config.SampleRate)
		
		var codec string
		switch config.BitDepth {
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
		
		isMp3 := actualCodec == "libmp3lame"
		
		if isMp3 {
			args = append(args, "-c:a", actualCodec)
		} else {
			args = append(args, "-ar", "48000")
			args = append(args, "-c:a", actualCodec)
		}
		
		needsFullNumber := (actualCodec == "libfdk_aac" || actualCodec == "libopus" || actualCodec == "libmp3lame")
		
		bitrateStr := config.Bitrate
		
		if needsFullNumber {
			if strings.Contains(config.Bitrate, "k") {
				bitrateStr = strings.ReplaceAll(config.Bitrate, "k", "000")
			} else if strings.Contains(config.Bitrate, "000") {
				bitrateStr = config.Bitrate
			} else {
				bitrateStr = config.Bitrate + "000"
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
		
		if needsFullNumber {
			args = append(args, "-b:a", fmt.Sprintf("%d", bitrate))
		} else {
			args = append(args, "-b:a", fmt.Sprintf("%dk", bitrate))
		}
	}
	
	// Add speech optimization for Opus
	if config.IsSpeech && actualCodec == "libopus" && !n.noTranscode.Checked {
		args = append(args, "-application", "voip")
	} else if !config.IsSpeech && actualCodec == "libopus" && !n.noTranscode.Checked {
		args = append(args, "-application", "audio")
	}
	
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
		targetTp = n.normalizeTargetTp.Text
	} 
	
	// Add two-pass loudnorm filter if enabled
	if config.UseLoudnorm {
		var filterChain string
		if config.IsSpeech {
			filterChain = fmt.Sprintf(
				"speechnorm=e=12.5:r=0.0001:l=1,loudnorm=I=%s:TP=%s:LRA=5.0:measured_I=%s:measured_TP=%s:measured_LRA=%s:measured_thresh=%s:linear=true",
				target, targetTp,
				measured["input_i"], measured["input_tp"], measured["input_lra"], measured["input_thresh"],
			)
		} else {
			filterChain = fmt.Sprintf(
				"loudnorm=I=%s:TP=%s:LRA=5.0:measured_I=%s:measured_TP=%s:measured_LRA=%s:measured_thresh=%s:offset=%s:linear=true",
				target, targetTp,
				measured["input_i"], measured["input_tp"], measured["input_lra"], measured["input_thresh"], measured["target_offset"],
			)
		}
		args = append(args, "-af", filterChain)
	}
	n.logStatus("input_tp from measurement: " + measured["input_tp"])
	
	rgTpFlt, err := strconv.ParseFloat(measured["input_tp"], 64)
	if err != nil {
		n.logStatus("ERROR parsing peak: " + err.Error())
	}
	
	rgTpInLin := math.Pow(10, rgTpFlt/20)
	n.logStatus(fmt.Sprintf("Peak in linear: %.6f", rgTpInLin))
	
	if actualCodec == "libfdk_aac" && config.writeTags && measured != nil {
		args = append(args, "-movflags", "use_metadata_tags")
	}
	
	if config.writeTags && measured != nil {
		inputI, _ := strconv.ParseFloat(measured["input_i"], 64)
		targetFloat, _ := strconv.ParseFloat(target, 64)
		gain := targetFloat - inputI
		
		args = append(args, 
			"-metadata", fmt.Sprintf("REPLAYGAIN_TRACK_GAIN=%.2f dB", gain),
			"-metadata", fmt.Sprintf("REPLAYGAIN_TRACK_PEAK=%.6f", rgTpInLin),
			"-metadata", "REPLAYGAIN_REFERENCE_LOUDNESS=" + target + " LUFS",
		)
	}
	
	args = append(args, "-y", outputPath)
	
	cmd := exec.Command(ffmpegPath, args...)
	
	
	if err := cmd.Run(); err != nil {
		n.logStatus(fmt.Sprintf("✗ Failed: %s - %v", filepath.Base(inputPath), err))
		return false
	}
	
	n.logStatus(fmt.Sprintf("✓ Success: %s", filepath.Base(inputPath)))
	n.logStatus("")
	n.logStatus(fmt.Sprintf("Your files can be found from %s. Thank you.", n.outputDir))
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
	audioExts := []string{".mp3", ".wav", ".flac", ".m4a", ".aac", ".ogg", ".opus", ".wma", ".aiff", ".ape"}
	
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
	switch name {
	case theme.ColorNameBackground:
		return color.RGBA{R: 0xf5, G: 0xf5, B: 0xf7, A: 0xff}
	case theme.ColorNameButton:
		return color.RGBA{R: 0x00, G: 0x7a, B: 0xff, A: 0xff}
	case theme.ColorNameDisabledButton:
		return color.RGBA{R: 0xcc, G: 0xcc, B: 0xcc, A: 0xff}
	case theme.ColorNameForeground:
		return color.RGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xff}
	case theme.ColorNameHover:
		return color.RGBA{R: 0xe5, G: 0xe5, B: 0xea, A: 0xff}
	case theme.ColorNameInputBackground:
		return color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	case theme.ColorNameInputBorder:
		return color.RGBA{R: 0xd1, G: 0xd1, B: 0xd6, A: 0xff}
	case theme.ColorNamePlaceHolder:
		return color.RGBA{R: 0x8e, G: 0x8e, B: 0x93, A: 0xff}
	case theme.ColorNamePressed:
		return color.RGBA{R: 0x00, G: 0x5a, B: 0xbf, A: 0xff}
	case theme.ColorNameSelection:
		return color.RGBA{R: 0x00, G: 0x7a, B: 0xff, A: 0x66}
	case theme.ColorNameMenuBackground:
		return color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	case theme.ColorNameOverlayBackground:
		return color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
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
	return theme.DefaultTheme().Font(style)
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