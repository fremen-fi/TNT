package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"fmt"
	"path/filepath"
	"strconv"
)

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
	})
	
	// Simple mode widgets
	n.simpleGroupButtons = widget.NewRadioGroup([]string{
		"Small file (AAC 256kbps)",
		"Most compatible (MP3 320kbps)",
		"Production (PCM 48kHz/24bit)",
	}, nil)
	n.simpleGroupButtons.SetSelected("Production (PCM 48kHz/24bit)")
	
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
	
	n.normalizeTarget.OnChanged = func(s string) {
		if n.loudnormCustomCheck.Checked {
			n.updateNormalizationLabel("Custom")
		}
	}
	
	n.normalizeTargetTp = widget.NewEntry()
	n.normalizeTargetTp.SetPlaceHolder("TP limit")
	n.normalizeTargetTp.SetText("-1")
	
	n.normalizeTargetTp.OnChanged = func(s string) {
		if n.loudnormCustomCheck.Checked {
			n.updateNormalizationLabel("Custom")
		}
	}
		
	// Loudnorm checkbox
	n.writeTagsLabel = widget.NewLabel("Write RG tags (EBU R128: -23 LUFS)")
	
	n.writeTags = widget.NewCheck("", func(checked bool) {
		if checked  && n.checkPCM(){
			n.loudnormCheck.Disable()
			n.noTranscode.Disable()
			n.noTranscode.SetChecked(false)
			n.noTranscode.Hide()
		} else if checked {
			n.loudnormCheck.Disable()
			n.loudnormCheck.SetChecked(false)
			n.noTranscode.Enable()
			n.noTranscode.Show()
		} else {
			n.loudnormCheck.Enable()
			n.noTranscode.Disable()
			n.noTranscode.SetChecked(false)
			n.noTranscode.Hide()
		}
	})
	
	writeTagsRow := container.NewHBox(n.writeTags, n.writeTagsLabel)
	n.writeTags.SetChecked(false)
	
	n.writeTags.SetChecked(false)
	n.writeTags.Disable()
	
	n.noTranscode = widget.NewCheck("Do not transcode", func(b bool) {
		if b {
			n.bypassProc.SetChecked(true)
			n.bypassProc.Disable()
		} else {
			n.bypassProc.Enable()
		}
	}) 
	n.noTranscode.SetChecked(false)
	n.noTranscode.Disable()
	n.noTranscode.Hide()
	
	n.dataCompLevel = widget.NewSlider(0, 10)
	n.dataCompLevel.Step = 1
		
	n.loudnormCustomCheck = widget.NewCheck("Custom loudness", func(checked bool) {
		if n.loudnormCustomCheck.Checked {
			n.normalizeTarget.Enable()
			n.normalizeTargetTp.Enable()
			n.normalizeTarget.Show()
			n.normalizeTargetTp.Show()
			n.normalizeTargetLabel.Show()
			n.normalizeTargetLabelTp.Show()
			n.updateNormalizationLabel("Custom")
		} else {
			n.normalizeTarget.Disable()
			n.normalizeTargetTp.Disable()
			n.normalizeTarget.Hide()
			n.normalizeTargetTp.Hide()
			n.normalizeTargetLabel.Hide()
			n.normalizeTargetLabelTp.Hide()
			n.updateNormalizationLabel(n.normalizationStandard)
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
	n.normalizeTargetLabel = widget.NewLabel("Target in LUFS")
	n.normalizeTargetLabelTp = widget.NewLabel("TP limit in dB")
	dataCompLevelLabel := widget.NewLabel("Set data compression level (0 is off)")
	dataCompLevelLabelCurrent := widget.NewLabel(fmt.Sprintf("Set: %d", int(n.dataCompLevel.Value)))
	
	n.normalizeTarget.Disable()
	n.normalizeTargetTp.Disable()
	n.normalizeTarget.Hide()
	n.normalizeTargetTp.Hide()
	n.normalizeTargetLabel.Hide()
	n.normalizeTargetLabelTp.Hide()
	
	n.dataCompLevel.OnChanged = func(f float64) {
		dataCompLevelLabelCurrent.SetText(fmt.Sprintf("Set: %d", int(f)))
	}
	
	n.IsSpeechCheck = widget.NewCheck("Optimize Opus for speech", func(checked bool){
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
		
		usesDataComp := value == "Opus" || value == "FLAC"
		usesBitDepth := value == "PCM"
		usesBitRate := value != "PCM" && value != "FLAC"
		usesSampleRate := value == "PCM"
		
		if usesDataComp {
			n.dataCompLevel.Show()
			dataCompLevelLabel.Show()
			dataCompLevelLabelCurrent.Show()
		} else {
			n.dataCompLevel.Hide()
			dataCompLevelLabel.Hide()
			dataCompLevelLabelCurrent.Hide()
		}
		
		if usesBitDepth {
			n.bitDepth.Show()
			bitDepthLabel.Show()
			bitrateLabel.Hide()
			n.bitrateEntry.Hide()
		} else {
			n.bitDepth.Hide()
			bitDepthLabel.Hide()
		}
		
		if usesBitRate {
			n.bitrateEntry.Show()
			bitrateLabel.Show()
		} else {
			n.bitrateEntry.Hide()
			bitrateLabel.Hide()
		}
		
		if usesSampleRate {
			n.sampleRate.Show()
			sampleRateLabel.Show()
		} else {
			n.sampleRate.Hide()
			sampleRateLabel.Hide()
		}
		
	})
	n.formatSelect.SetSelected(getPlatformFormats()[1])
	
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
	
	n.modeWarning = widget.NewLabel("To use advanced features, trigger processing from Advanced or Processing view.")
	n.modeWarning.Wrapping = fyne.TextWrapWord
	
	n.simpleGroup = container.NewVBox(n.modeWarning, n.simpleGroupButtons, loudnormRow)
		
	n.advancedContainer = container.NewVBox(
		container.NewBorder(nil, nil, formatLabel, nil, widget.NewLabel("")),
		container.NewBorder(nil, nil, sampleRateLabel, nil, n.sampleRate),
		container.NewBorder(nil, nil, bitDepthLabel, nil, n.bitDepth),
		container.NewBorder(nil, nil, bitrateLabel, nil, n.bitrateEntry),
		container.NewBorder(nil, nil, n.normalizeTargetLabel, nil, n.normalizeTarget),
		container.NewBorder(nil, nil, n.normalizeTargetLabelTp, nil, n.normalizeTargetTp),
		container.NewBorder(nil,nil, dataCompLevelLabel, dataCompLevelLabelCurrent, n.dataCompLevel),
		
		n.loudnormCustomCheck,
		writeTagsRow,
		n.noTranscode,
		loudnormRow,
		n.IsSpeechCheck,
	)
	
	// Replace placeholder with actual format select
	n.advancedContainer.Objects[0] = container.NewBorder(nil, nil, formatLabel, nil, n.formatSelect)
	
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
	
	// processing tab 
	n.dynamicsLabel = widget.NewLabel("Dynamics processing level")
	n.dynamicsDrop = widget.NewSelect([]string{"Off", "Light", "Moderate", "Broadcast"}, nil)
	n.dynamicsDrop.SetSelected("Off")
	dynamicsRow := container.NewHBox(n.dynamicsDrop, n.dynamicsLabel)
	
	n.EqLabel = widget.NewLabel("EQ target curve")
	n.EqDrop = widget.NewSelect([]string{"Off", "Flat", "Speech", "Broadcast"}, nil)
	n.EqDrop.SetSelected("Off")
	eqRow := container.NewHBox(n.EqDrop, n.EqLabel)
	
	n.bypassProc = widget.NewCheck("Bypass all processing", func(checked bool) {
		if checked {
			n.dynamicsDrop.Disable()
			n.EqDrop.Disable()
		} else {
			n.dynamicsDrop.Enable()
			n.EqDrop.Enable()
		}
	})
	
	n.dynNorm = widget.NewCheck("", nil)
	n.dynNormLabel = widget.NewLabel("Use dynamic normalization")
	dynNormRow := container.NewHBox(n.dynNorm, n.dynNormLabel)
	
	processTab := container.NewVBox(dynamicsRow, eqRow, dynNormRow, widget.NewSeparator(), n.bypassProc)
	
	checkUpdateButton := widget.NewButton("Check for updates", func() {
		go checkForUpdates(currentVersion, n.window, n.logFile)
	})
	
	helpBtn := widget.NewButton("Help", func() {
			
			menuGettingStarted := widget.NewLabel(				
`TNT is designed for broadcast professionals to streamline audio workflows. The application provides three core capabilities:

• Transcode - Convert between audio formats
• Normalize - Ensure consistent loudness levels  
• Tag - Write ReplayGain metadata for playback guidance

FAST MODE
Simple mode offers three preset configurations for common use cases. Processing requires just four clicks, and files are processed individually in the background with results appearing in your output folder as they complete.

ADVANCED MODE
Advanced mode provides granular control over encoding parameters including format selection, sample rates, bit depths, and bitrates. You can configure custom loudness normalization targets or write ReplayGain tags instead of normalizing.

Note: Normalization alters the audio data, while tagging only writes metadata. These options are mutually exclusive.

WORKFLOW
1. Select Files - Choose individual files, or Select Folder for batch processing
2. Output Folder - Specify destination for processed files
3. Configure settings in Fast or Advanced mode
4. Click Process

For more information visit https://www.fremen.fi/software/tnt and scroll to the bottom of the page.`)
			menuGettingStarted.Wrapping = fyne.TextWrapWord
			
			menuSimpleTab := widget.NewLabel(`
SIMPLE MODE

Simple mode provides three preset configurations optimized for common broadcast scenarios:

• Small file (AAC 256kbps) - Compressed format balancing quality and file size
• Most compatible (MP3 320kbps) - Universal playback support across all devices
• Production (PCM 48kHz/24bit) - Uncompressed broadcast-quality audio

Each preset handles format conversion with minimal configuration required. Simply select your desired output format from the three options.

NORMALIZATION
The 'Normalize' checkbox applies EBU R128 loudness normalization to your audio files. When enabled, all processed files will meet the -23 LUFS standard with -1 dBTP limiting, ensuring consistent playback levels across your content.

WORKFLOW
Processing in Simple mode requires just four clicks:
1. Select your files or folder
2. Choose output destination
3. Pick a preset format
4. Click Process

The application processes files individually in the background. Completed files appear in your output folder as they finish, allowing you to continue working while processing continues.`)
			menuSimpleTab.Wrapping = fyne.TextWrapWord
			
			menuAdvancedTab := widget.NewLabel(
`ADVANCED MODE

Advanced mode provides granular control over all encoding parameters.

FORMAT SELECTION
Choose from AAC, Opus, MP3, PCM (Wave), or FLAC.

Sample Rate: Available only for PCM (44.1 - 192 kHz)
Bit Depth: Available only for PCM (16, 24, 32-float, 64-float)
Bitrate: Available for AAC, Opus, and MP3 (12 kbps minimum, encoder-specific maximum)
Compression Level: Available for FLAC and Opus (slider from 0-10)
• 0 = no compression
• 10 = most compression

LOUDNESS TARGETS
Target in LUFS and TP limit in dB control loudness processing for both normalization and ReplayGain tagging.

Custom Loudness: When enabled, you can configure custom LUFS I and TP targets. Values are automatically converted to negative. When disabled, the system uses EBU R128 defaults (-23 LUFS, -1 dBTP) if Normalize is selected.

PROCESSING OPTIONS

Normalize: Applies loudness correction using the BS.1770-5 algorithm
• Uses custom values if Custom Loudness is enabled
• Uses EBU R128 standard if Custom Loudness is disabled
• Alters the audio data to match target loudness

Write RG tags: Writes ReplayGain metadata to audio files
• Uses custom values if Custom Loudness is enabled, otherwise EBU R128
• Does not alter audio data, only writes metadata
• Cannot be used with Normalize (mutually exclusive)
• Cannot be used with PCM source files

Do not transcode: Preserves original audio encoding while writing tags
• Only available when Write RG tags is enabled
• Does not alter audio data, only writes metadata
• Cannot be used with PCM source files
• Useful for adding metadata without re-encoding
• Checking this box disables processing

Speech: Optimizes encoding for voice content
• Automatically selects Opus codec
• Applies VoIP-optimized compression settings
• Uses speech-specific normalization when combined with Normalize
• Do not use with music content`)
			menuAdvancedTab.Wrapping = fyne.TextWrapWord
			
			menuFormatsTab := widget.NewLabel(
`AUDIO FORMATS

AAC (Advanced Audio Coding)
AAC is a data compression method that at high bitrates can sound similar to a non-compressed file. In simple mode, the bitrate is set to 256 kbit/s, which gives very good results. The maximum bitrate for this encoder is 512 kbit/s. At 320 kbit/s the encoder tends to lose almost all of its encoding artifacts. Thirty seconds of audio encoded with 256 kbit/s results in approximately 1 MB filesize.

Two AAC encoders are available depending on platform:
• Fraunhofer FDK-AAC (all platforms) - Industry-standard reference encoder
• Apple AudioToolbox AAC (macOS only) - Native hardware-accelerated encoder optimized for Apple Silicon

Opus
Opus is a modern data compression method that can achieve very good results even with lower bitrates. Opus has a lower algorithmic delay, which makes it suitable for live applications. It's an open-source format. Its minimum bitrate is 6 kbit/s, though the UI limits the bitrate at 12 kbit/s at minimum. The maximum bitrate for this encoder is 510 kbit/s.

MP3 (MPEG-I Layer 3)
MP3 is an older, but one of the most compatible encoders available. It isn't as capable at lower bitrates as the two encoders above, but at high bitrates (>320 kbit/s) it's usable. Use this if you know the end-user can't decode AAC or Opus. Filesize for MP3 at 320 kbit/s for 30 second audio file is 1.2 MB.

FLAC (Free Lossless Audio Codec)
FLAC is a lossless compression format that reduces file size without any quality loss. Unlike AAC, Opus, or MP3, FLAC preserves the original audio data perfectly while still achieving significant compression. File sizes are typically 40-60% of uncompressed PCM, depending on the compression level selected. FLAC is widely supported and ideal for archival or when perfect audio fidelity is required with reasonable file sizes.

PCM (WAV)
PCM, or WAV in this tool is a pulse-code modulated, raw uncompressed audio stream. It's the highest quality, but it comes with a size-cost. This encoder doesn't have a bitrate setting, but has two other settings that result in a bitrate. First, sample rate (either 44.1, 48, 88.2, 96, 192 kHz) means "how often the original data is converted into audio in a second". With 48 kHz the audio is sampled forty-eight thousand times in a second. Second, the bit depth controls "how precisely we want to have each sample". The options are either 16, 24, 32 or 64, of which the last two are floating-point and used in specific scenarios. The file size for a thirty-second audio with 48 kHz, 24-bit audio is 8.64 MB.`)
			menuFormatsTab.Wrapping = fyne.TextWrapWord
			
			menuProcessingTab := widget.NewLabel(
`
Setting 'Do not transcode' in the Advanced tab bypasses all processing.

Dynamics processing
Dynamics processing controls how TNT manages the volume variations in your audio. The software analyzes peak levels, average energy, and dynamic range before applying any processing. While designed for spoken content, dynamic processing may deliver pleasing results when used on music content. The first two presets are usually relatively transparent, with the last "Broadcast" preset being an aggressive multi-band compressor.

TNT uses a Dynamic Scoring system in determining the characteristics of each compression preset. The user shall choose the style of compression, while the program decides what exact values are used within each preset. The amount of compression will always increase when choosing a higher processing tier.

Off No dynamics processing is applied. Use this when your audio is already properly compressed or when you need the original dynamics preserved.

Light Gentle compression that reduces only the loudest peaks. The software identifies peak RMS levels and applies subtle compression with a 2.5:1 ratio. Attack and release times are set to preserve transients while smoothing out occasional loud moments. This preset maintains the natural character of your audio while preventing clipping.

Light processing is appropriate for: well-recorded content that needs minimal adjustment, acoustic music where dynamics are intentional, and content where natural dynamics should be preserved.

Using Light on a music track, whose Dynamic Score is 18.64, results in a new Dynamic Score of 17.33.

Moderate Standard broadcast compression suitable for most content. TNT analyzes your audio's average RMS level and applies moderate compression with a 3.5:1 ratio. The software calculates makeup gain automatically based on how much compression is being applied, ensuring consistent output levels without manual adjustment.

Moderate processing works for: podcasts, voice-overs, most music content, and general broadcast material that needs to sound consistent across different playback systems.

For the same example track as in Light preset above, the new Dynamic Score is 14.58.

Broadcast Aggressive multiband processing for maximum clarity and consistency. Instead of analyzing overall dynamics, the software splits your audio into five frequency bands (sub-bass, bass, low-mid, mid, and high) and analyzes each band independently. Each band receives compression tailored to its specific characteristics—bass frequencies get tighter control with longer attack times, while high frequencies receive faster compression to maintain clarity.

The Broadcast preset uses adaptive ratios: bass content receives moderate compression (4.0:1 ratio) while high-frequency content gets more aggressive processing (up to 8.0:1 ratio). This ensures your audio maintains punch in the low end while achieving maximum intelligibility in the speech range. Attack and release times are frequency-dependent, ranging from 200ms in the bass to 100ms in the highs.

Broadcast processing is designed for: radio content, streaming platforms with varied playback systems, content consumed on small speakers or mobile devices, and any situation where maximum loudness and clarity are required.

Broadcasting preset will deliver varying results with music. For heavily compressed material (DS <9), distortion is likely to occur.

For the same track as in two previous presets, the new Dynamic Score is 12.71

EQ target curves
EQ processing analyzes your audio's frequency response across ten octave-spaced bands from 50Hz to 12.8kHz+. The software measures RMS level, peak level, and crest factor for each band, then compares these measurements against professional target curves. All EQ adjustments use an attenuation-focused philosophy—corrections are calculated, then halved before application, with a maximum adjustment of ±10 dB. This conservative approach maintains audio quality while achieving broadcast standards. Equalization is designed to work with spoken content. It will delivery varying results when used with music.

Off
No equalization is applied.

Flat
Targets a pink noise curve, which naturally contains more energy in the bass frequencies (-3 dB per octave rise from 1kHz). The Flat preset attenuates frequencies that exceed this curve while leaving frequencies below the curve unchanged. This prevents excessive energy buildup in any frequency range while maintaining the natural tonal balance of your content.

Flat EQ is appropriate for material that's already well-balanced, and situations where you want to prevent frequency buildup without imposing a specific tonal character.

Speech
Optimized for vocal clarity and intelligibility. The Speech preset boosts presence frequencies (1.6kHz-6.4kHz) where consonants and speech intelligibility live, while attenuating sub-bass rumble and reducing boxiness in the 200-400Hz range.

Speech EQ is designed for: podcasts, voice-overs, audiobooks, conference recordings, interviews, and any content where vocal clarity is paramount.

Broadcast
Aggressive clarity enhancement for playback on small speakers, mobile devices, and varied listening environments. The Broadcast curve emphasizes midrange intelligibility even more strongly than Speech mode, with deeper cuts in the bass and more aggressive presence boost. This ensures your content cuts through on phone speakers, laptop audio, and car radios.

Broadcast EQ is intended for: radio content, streaming platforms, mobile-first content, situations where playback systems are unknown, and any content that must remain intelligible on poor speakers.

Bypass all processing
When enabled, this checkbox disables both Dynamics and EQ processing regardless of their selected settings. Use this when you want loudness normalization only, without any dynamics control or tonal shaping. The Bypass option is useful for: testing how your audio sounds with normalization alone, A/B comparing processed versus unprocessed versions, or situations where you've already applied processing in your DAW and only need format conversion and loudness compliance.

Processing order
When multiple processing stages are enabled, TNT applies them in this order:

EQ adjustments (if enabled)
De-esser (automatically applied when EQ is active)
Dynamic normalization
Dynamics processing (if enabled)
Loudness normalization (if enabled)
This signal chain ensures frequency balance is corrected before dynamics processing, preventing the compressor from reacting to frequency imbalances. The de-esser removes harsh sibilance after EQ boosts but before compression, ensuring the compressor doesn't overreact to "s" sounds. Loudness normalization happens last, after all processing is complete, guaranteeing your target LUFS level is achieved accurately.

Notes
All processing happens at 192kHz sample rate internally to ensure intersample peak accuracy. For 16-bit PCM output, the software applies triangular dithering after all processing to minimize quantization artifacts. Multiband processing uses linear-phase crossover filters to prevent phase distortion between frequency bands.

The adaptive nature of TNT's processing means two identical preset selections may produce different filter parameters depending on the input audio's characteristics. This is intentional — the software adjusts its processing based on what it measures, ensuring optimal results for each file rather than applying static presets that may not suit the content.
`)
		menuProcessingTab.Wrapping = fyne.TextWrapWord
		
		menuWatchHelpTab := widget.NewLabel(
`
Watch mode automates repetitive processing tasks by monitoring a folder and automatically processing new files as they appear. For example, a newsdesk can configure TNT to watch their raw audio folder - whenever a reporter records new audio, TNT detects it within seconds and outputs the processed file to the specified destination. TNT must remain running (the window can be minimized or hidden).

Watch mode uses your current UI settings. To change processing parameters, simply adjust the settings in the interface - all subsequent files will use the new configuration. Save your preferences to automatically restore your settings on startup.

Watch mode only processes new files added after activation - it ignores existing files. To process a folder's current contents, select it via "Select Folder" first. Once complete, enable Watch mode to handle any newly added files.
`)
		menuWatchHelpTab.Wrapping = fyne.TextWrapWord
			
			tabs := container.NewAppTabs(
				container.NewTabItem("Getting started", container.NewScroll(menuGettingStarted)),
				container.NewTabItem("Simple", container.NewScroll(menuSimpleTab)),
				container.NewTabItem("Advanced", container.NewScroll(menuAdvancedTab)),
				container.NewTabItem("Processing", container.NewScroll(menuProcessingTab)),
				container.NewTabItem("Watcher", container.NewScroll(menuWatchHelpTab)),
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
		lufsEntry.OnChanged = func(s string) {
			if stdGroup.Selected == "Custom" {
				n.normalizeTarget.SetText(s)
				n.updateNormalizationLabel("Custom")
			}
		}
		
		tpEntry := widget.NewEntry()
		tpEntry.SetText(n.normalizeTargetTp.Text)
		tpEntry.Validator = func(s string) error {
			if s == "" || s == "-" {
				return nil
			}
			val, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return fmt.Errorf("must be a number")
			}
			if val > 0 {
				return fmt.Errorf("must be less than or exactly zero")
			}
			return nil
		}
		tpEntry.OnChanged = func(s string) {
			if stdGroup.Selected == "Custom" {
				n.normalizeTargetTp.SetText(s)
				n.updateNormalizationLabel("Custom")
			}
		}
		
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
		
		tpRow := container.NewVBox(tpEntry)
		
		normInstructions := widget.NewLabel("Values are interpreted as negative values regardless of input. Empty values default to -23 LUFS and -1 dBTP.")
		normInstructions.Wrapping = fyne.TextWrapWord
		
		normContent := container.NewVBox(
			normInstructions,
			widget.NewLabel("Default normalization targets:"),
			stdGroup,
			widget.NewLabel("Custom LUFS target:"),
			lufsEntry,
			widget.NewLabel("Custom TP target:"),
			tpRow,
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
		
		userFactoryResetBtn := widget.NewButton("Reset to defaults", func() {
			dialog.ShowConfirm("Reset preferences",
		"This will delete all saved preferences. TNT will use default settings on next launch. Continue?",
		func(b bool) {
			if b {
				n.resetPreferences()
			}
		},
		n.window,
		)
		})
		
		saveContent := container.NewVBox(
			saveContentText,
			widget.NewSeparator(),
			saveBtn,
			widget.NewSeparator(),
			userFactoryResetBtn,
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
	
	previewSizeBtn := widget.NewButton("Preview Size", func() {
		n.previewSize()
	})
	
	topButtons := container.NewHBox(selectFilesBtn, selectFolderBtn)
	outputSection := container.NewBorder(nil, nil, widget.NewLabel("Output:"), selectOutputBtn, n.outputLabel)
	
	topBar := container.NewHBox(helpBtn, menuBtn)
	
	modeTabs := container.NewAppTabs(
		container.NewTabItem("Fast", container.NewPadded(n.simpleGroup)),
		container.NewTabItem("Advanced", container.NewPadded(n.advancedContainer)),
		container.NewTabItem("Processing", container.NewPadded(processTab)),
	)
	
	n.modeTabs = modeTabs
	
	// Layout
	settingsContainer := container.NewVBox(
		n.watcherWarnLabel,
		logoImg,
		topBar,
		//n.modeToggle,
		widget.NewSeparator(),
		topButtons,
		outputSection,
		widget.NewSeparator(),
		modeTabs,
		//n.simpleGroup,
		//n.advancedContainer,
	)
	
	content := container.NewBorder(
		container.NewVBox(
			settingsContainer,
			widget.NewSeparator(),
		),
		container.NewVBox(
			n.progressBar,
			container.NewPadded(container.NewHBox(n.processBtn, clearAllBtn, previewSizeBtn)),
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
}