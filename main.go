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
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/fsnotify/fsnotify"
)

const currentVersion = "1.0.2"

type VersionInfo struct {
	Version      string `json:"version"`
	DownloadURL  string `json:"download_url"`
	ReleaseNotes string `json:"release_notes"`
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
						cmd.Start()
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
	
	// Compare each part numerically
	for i := 0; i < 3; i++ {
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
	os.WriteFile(ffmpegPath, ffmpegBinary, 0755)
	return ffmpegPath
}

var ffmpegPath string

func init() {
	ffmpegPath = extractFFmpeg()
}

func (n *AudioNormalizer) initLogFile() *os.File {
	configDir, _ := os.UserConfigDir()
	logPath := filepath.Join(configDir, "TNT", "tnt.log")
	
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
		cmd := exec.Command("cmd", "/c", "start", mailtoURL)
		hideWindow(cmd)
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
	loudnormLabel *widget.Label
	normalizationStandard string
	IsSpeechCheck *widget.Check
	writeTags *widget.Check
	noTranscode *widget.Check
	
	logFile *os.File
	
	// watchmode
	watchMode *widget.Check
	watching bool
	watcherStop chan bool
	jobQueue chan string
	inputDir string
	watcherWarnLabel *widget.Label
	
	watcherMutex sync.Mutex
	
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
	n.simpleGroup.SetSelected(prefs.SimpleMode)
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
}

func (n *AudioNormalizer) savePreferences() {
	prefs := Preferences{
		AdvancedMode: n.advancedMode,
		LastOutputDir: n.outputDir,
		SimpleMode: n.simpleGroup.Selected,
		Format: n.formatSelect.Selected,
		SampleRate: n.sampleRate.Selected,
		BitDepth: n.bitDepth.Selected,
		Bitrate: n.bitrateEntry.Text,
		LoudnormEnabled: n.loudnormCheck.Checked,
		CustomLoudnorm: n.loudnormCustomCheck.Checked,
		NormalizeTarget: n.normalizeTarget.Text,
		NormalizeTargetTp: n.normalizeTargetTp.Text,
		NormalizationStandard: n.normalizationStandard,
	}
	
	configDir, _ := os.UserConfigDir()
	prefsDir := filepath.Join(configDir, "TNT")
	os.MkdirAll(prefsDir, 0755)
	
	data, _ := json.MarshalIndent(prefs, "", "  ")
	os.WriteFile(filepath.Join(prefsDir, "preferences.json"), data, 0644)
}

func (n *AudioNormalizer) updateNormalizationLabel(standard string) {
	switch standard {
		case "EBU R128 (-23 LUFS)":
			n.loudnormLabel.SetText("Normalize (EBU R128: -23 LUFS)")
		case "USA ATSC A/85 (-24 LUFS)":
			n.loudnormLabel.SetText("Normalize (ATSC A/85: -24 LUFS)")
		case "Custom":
			target := n.normalizeTarget.Text
			n.loudnormLabel.SetText(fmt.Sprintf("Normalize (Custom %s LUFS)", target))
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

func (n *AudioNormalizer) setupUI(a fyne.App) {
	logoImg := canvas.NewImageFromResource(getLogoForTheme(a))
	logoImg.SetMinSize(fyne.NewSize(0, 100))
	logoImg.FillMode = canvas.ImageFillContain
	
	go func() {
		a.Settings().AddListener(func(s fyne.Settings) {
			logoImg.Resource = getLogoForTheme(a)
			canvas.Refresh(logoImg)
		})
	}()
	
	n.fileList = widget.NewList(
		func() int { return len(n.files) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil, nil, 
				widget.NewButtonWithIcon("", theme.DeleteIcon(), nil),
				widget.NewLabel("template"),
			)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			border := o.(*fyne.Container)
			label := border.Objects[0].(*widget.Label)
			btn := border.Objects[1].(*widget.Button)
			
			label.SetText(filepath.Base(n.files[i]))
			btn.OnTapped = func() {
				n.removeFile(i)
			}
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
			n.noTranscode.Disable()
			n.noTranscode.SetChecked(false)
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
		}
	})
	n.loudnormCustomCheck.SetChecked(false)
	n.normalizeTarget.Disable()
	n.normalizeTargetTp.Disable()
	
	n.watchMode = widget.NewCheck("Watch", func(checked bool) {
		if checked {
			n.startWatching()
			n.watcherWarnLabel.SetText("WATCHING")
		} else {
			n.stopWatching()
			n.watcherWarnLabel.SetText("")
		}
	})
	n.watchMode.SetChecked(false)
	
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
	n.formatSelect = widget.NewSelect(getPlatformFormats(), func(value string) {
		n.updateAdvancedControls()
	})
	n.formatSelect.SetSelected(getPlatformFormats()[1])
	
	// Replace placeholder with actual format select
	n.advancedContainer.Objects[0] = container.NewBorder(nil, nil, formatLabel, nil, n.formatSelect)
	
	// Loudnorm checkbox
	n.loudnormLabel = widget.NewLabel("Normalize (EBU R128: -23 LUFS)")
	n.loudnormCheck = widget.NewCheck("", func(checked bool) {
		if checked {
			n.writeTags.Disable()
		} else {
			n.writeTags.Enable()
		}
	})
	loudnormRow := container.NewHBox(n.loudnormCheck, n.loudnormLabel)
	n.loudnormCheck.SetChecked(false)
	
	n.normalizationStandard = "EBU R128 (-23 LUFS)"
	
	n.watcherWarnLabel = widget.NewLabel("")
	
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
	
	checkUpdateButton := widget.NewButton("Check for updates", func() {
		go checkForUpdates(currentVersion, n.window, n.logFile)
	})
	
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
	
	menuBtn := widget.NewButton("Menu", func() {
		n.menuMutex.Lock()
		if n.menuWindow != nil {
			n.menuMutex.Unlock()
			n.menuWindow.RequestFocus()
			return
		}
		n.menuMutex.Unlock()
		// Create normalization settings content
		stdGroup := widget.NewRadioGroup([]string{"EBU R128 (-23 LUFS)", "USA ATSC A/85 (-24 LUFS)", "Custom"}, nil)
		stdGroup.SetSelected(n.normalizationStandard)
		
		lufsEntry := widget.NewEntry()
		lufsEntry.SetText(n.normalizeTarget.Text)
		
		tpEntry := widget.NewEntry()
		tpEntry.SetText(n.normalizeTargetTp.Text)
		
		stdGroup.OnChanged = func(selected string) {
			if selected == "Custom" {
				lufsEntry.Enable()
				tpEntry.Enable()
			} else {
				lufsEntry.Disable()
				tpEntry.Disable()
				
				// Update immediately when standard changes
				switch selected {
				case "EBU R128 (-23 LUFS)":
					n.normalizeTarget.SetText("-23")
					n.normalizeTargetTp.SetText("-1")
					lufsEntry.SetText("-23")
					tpEntry.SetText("-1")
				case "USA ATSC A/85 (-24 LUFS)":
					n.normalizeTarget.SetText("-24")
					n.normalizeTargetTp.SetText("-2")
					lufsEntry.SetText("-24")
					tpEntry.SetText("-2")
				}
				n.updateNormalizationLabel(selected)
				n.normalizationStandard = selected
			}
		}
		
		if stdGroup.Selected != "Custom" {
			lufsEntry.Disable()
			tpEntry.Disable()
		}
		
		normContent := container.NewVBox(
			widget.NewLabel("Default normalization targets:"),
			stdGroup,
			widget.NewLabel("Custom LUFS target:"),
			lufsEntry,
			widget.NewLabel("Custom TP target:"),
			tpEntry,
		)
		
		// Create save button content
		saveBtn := widget.NewButton("Save current configuration", func() {
			// Apply normalization settings
			switch stdGroup.Selected {
			case "EBU R128 (-23 LUFS)":
				n.normalizeTarget.SetText("-23")
				n.normalizeTargetTp.SetText("-1")
				lufsEntry.SetText("-23")
				tpEntry.SetText("-1")
			case "USA ATSC A/85 (-24 LUFS)":
				n.normalizeTarget.SetText("-24")
				n.normalizeTargetTp.SetText("-2")
				lufsEntry.SetText("-24")
				tpEntry.SetText("-2")
			case "Custom":
				n.normalizeTarget.SetText(lufsEntry.Text)
				n.normalizeTargetTp.SetText(tpEntry.Text)
			}
			n.updateNormalizationLabel(stdGroup.Selected)
			n.normalizationStandard = stdGroup.Selected
			
			n.savePreferences()
			dialog.ShowInformation("Saved", "Preferences saved successfully", n.window)
		})
		
		saveContentText := widget.NewLabel(`
Save all current settings, including Mode (simple/advanced), Format and encoding settings, Normalization defaults and last output directory. Preferences are loaded automatically on startup.
			`)
		saveContentText.Wrapping = fyne.TextWrapWord
		
		saveContent := container.NewVBox(
			saveContentText,
			widget.NewSeparator(),
			saveBtn,
		)
				
		versionUpdate := container.NewVBox(
			widget.NewLabel("Check for updates"),
			widget.NewLabel(fmt.Sprintf("You're currently running version %s", currentVersion)),
			widget.NewSeparator(),
			checkUpdateButton,
		)
		
		settingsWatchModeText := widget.NewLabel(`
Start watch mode
Watch mode processes new files in a directory automatically.
Origin directory is selected from main UI by clicking 'Select Folder' and the output directory is chosen via 'Select Output'. Watch mode doesn't process files already existing in a directory. To trigger processing by watcher, files need to spawn to the watched directory.
Watch mode status is indicated by a text in the top left corner. If empty, watch mode is OFF.
			`)
			
		settingsWatchModeText.Wrapping = fyne.TextWrapWord
		
		settingsWatchMode := container.NewVBox(
			settingsWatchModeText,
			widget.NewSeparator(),
			n.watchMode,
		)
		
		settingsSendErrorReportText := widget.NewLabel(`
Send an error report.
			`)
			
			settingsSendErrorReportText.Wrapping = fyne.TextWrapWord
			
		sendLogReportBtn := widget.NewButton("Send report", func() {
			n.sendLogReport()
		})
			
		settingsSendErrorReport := container.NewVBox(
			settingsSendErrorReportText,
			widget.NewSeparator(),
			sendLogReportBtn,
			
		)
		
		tabs := container.NewAppTabs(
			container.NewTabItem("Normalization", normContent),
			container.NewTabItem("Save Configuration", saveContent),
			container.NewTabItem("Watch mode", settingsWatchMode),
			container.NewTabItem("Version upgrade", versionUpdate),
			container.NewTabItem("Send error report", settingsSendErrorReport),
		)			
		
		prefsWindow := fyne.CurrentApp().NewWindow("Preferences")
		prefsWindow.SetContent(tabs)
		prefsWindow.Resize(fyne.NewSize(500, 400))
		
		n.menuWindow = prefsWindow
		prefsWindow.SetOnClosed(func() {
			n.menuMutex.Lock()
			n.menuWindow = nil
			n.menuMutex.Unlock()
		})
		
		prefsWindow.Show()
	})
	
	clearAllBtn := widget.NewButton("Clear all", func() {
		n.mutex.Lock()
		n.files = make([]string, 0)
		n.mutex.Unlock()
		n.fileList.Refresh()
		n.updateProcessButton()
		n.logStatus("Cleared all files from queue")
	})
	
	topButtons := container.NewHBox(selectFilesBtn, selectFolderBtn)
	outputSection := container.NewBorder(nil, nil, widget.NewLabel("Output:"), selectOutputBtn, n.outputLabel)
	
	topBar := container.NewHBox(helpBtn, menuBtn)
	
	// Layout
	settingsContainer := container.NewVBox(
		n.watcherWarnLabel,
		logoImg,
		topBar,
		n.modeToggle,
		widget.NewSeparator(),
		topButtons,
		outputSection,
		widget.NewSeparator(),
		n.simpleGroup,
		n.advancedContainer,
		loudnormRow,
	)
	
	content := container.NewBorder(
		container.NewVBox(
			settingsContainer,
			widget.NewSeparator(),
		),
		container.NewVBox(
			n.progressBar,
			container.NewHBox(n.processBtn, clearAllBtn),
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
		originIsAAC: n.checkOriginAAC(),
		writeTags: n.writeTags.Checked,
		noTranscode: n.noTranscode.Checked,
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
			config.Format = "AAC"
			config.Bitrate = "256"
		case "Most compatible (MP3 160kbps)":
			config.Format = "MPEG-II L3"
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
	
	if platformCodec := getPlatformCodecMap()[config.Format]; platformCodec != "" {
		actualCodec = platformCodec
	} else if codecMap[config.Format] != "" {
		actualCodec = codecMap[config.Format]
	}
	
	n.logToFile(n.logFile, fmt.Sprintf("DEBUG: config.Format=%s, actualCodec=%s", config.Format, actualCodec))
	
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
	
	if config.UseLoudnorm {
		outputPath = filepath.Join(outputDir, fmt.Sprintf("%s.normalized%s", baseName, ext))
	} else if config.writeTags && config.noTranscode {
		outputPath = filepath.Join(outputDir, fmt.Sprintf("%s.tagged%s", baseName, originalExt))
	} else if config.writeTags {
		outputPath = filepath.Join(outputDir, fmt.Sprintf("%s.tagged%s", baseName, ext))
	} else {
		outputPath = filepath.Join(outputDir, fmt.Sprintf("%s%s", baseName, ext))
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
	args := []string{"-i", inputPath, "-vn"}
	
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
		
		needsFullNumber := (actualCodec == "libfdk_aac" || actualCodec == "aac" || actualCodec == "libopus" || actualCodec == "libmp3lame")
		
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
	
	var rgTpInLin float64
	
	if config.writeTags {
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
	
	resultsInM4A := (actualCodec == "libfdk_aac" || actualCodec == "aac") || (config.originIsAAC && config.noTranscode)
	useMovFlags :=  resultsInM4A && config.writeTags && measured != nil 
	
	if useMovFlags {
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
	
	fullCmdLog := ffmpegPath + " " + strings.Join(args, " ")
	n.logToFile(n.logFile, fullCmdLog)	
	
	cmd := exec.Command(ffmpegPath, args...)
	hideWindow(cmd)
	
	if config.BitDepth != "" {
		n.logToFile(n.logFile, fmt.Sprintf("config.Bitdepth= %s", config.BitDepth))
	}
	
	if config.Bitrate != "" {
		n.logToFile(n.logFile, fmt.Sprintf("config.Bitrate= %s", config.Bitrate))
	}
	
	if config.SampleRate != "" {
		n.logToFile(n.logFile, fmt.Sprintf("config.SampleRate= %s", config.SampleRate))
	}
	
	if config.Format != "" {
		n.logToFile(n.logFile, fmt.Sprintf("config.Format= %s", config.Format))
	}
	
	if config.CustomLoudnorm {
		n.logToFile(n.logFile, fmt.Sprintf("Custom loudness values input and used:"))
		n.logToFile(n.logFile, fmt.Sprintf("LUFS I target: %s", target))
		n.logToFile(n.logFile, fmt.Sprintf("TP target: %s", targetTp))
	} 
	
	if config.writeTags && config.noTranscode {
		n.logToFile(n.logFile, "Writing tags and not transcoding")
		n.logToFile(n.logFile, fmt.Sprintf("Original format is: %s", originalExt))
		n.logToFile(n.logFile, fmt.Sprintf("LUFS I target: %s", target))
		n.logToFile(n.logFile, fmt.Sprintf("TP target: %s", targetTp))
	} else if config.writeTags {
		n.logToFile(n.logFile, fmt.Sprintf( "Writing tags and transcoding to %s", config.Format))
		n.logToFile(n.logFile, fmt.Sprintf("LUFS I target: %s", target))
		n.logToFile(n.logFile, fmt.Sprintf("TP target: %s", targetTp))
	}
	
	if err := cmd.Run(); err != nil {
		n.logStatus(fmt.Sprintf("✗ Failed: %s - %v", filepath.Base(inputPath), err))
		n.logToFile(n.logFile, fmt.Sprintf("Failed %s - %v", filepath.Base(inputPath), err))
		return false
	}
	
	n.logStatus(fmt.Sprintf("✓ Success: %s", filepath.Base(inputPath)))
	n.logToFile(n.logFile, fmt.Sprintf("✓ Success: %s", filepath.Base(inputPath)))
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
	hideWindow(cmd)
	
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
	hideWindow(cmd)
	
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
	return resourceBrockmannRegularTtf
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