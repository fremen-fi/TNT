package main

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// FrequencyBand represents analyzed frequency response data for one band
type FrequencyBand struct {
	Frequency   string  // e.g. "50Hz", "100Hz", "12.8kHz+"
	FilterType  string  // "lowpass", "bandpass", "highpass"
	RMSLevel    float64 // Average level in dB
	PeakLevel   float64 // Peak level in dB (for reference)
	CrestFactor float64 // Peak-to-RMS ratio
}

// analyzeFrequencyResponseBands analyzes the frequency response across 10 bands
// using lowpass, bandpass, and highpass filters with astats
func (n *AudioNormalizer) analyzeFrequencyResponseBands(inputPath string) []FrequencyBand {
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

	n.logStatus("Analyzing frequency response across 10 bands...")
	n.logToFile(n.logFile, "Starting frequency response analysis")

	for i := range bands {
		band := &bands[i]
		
		var filterChain string
		switch band.FilterType {
		case "lowpass":
			// Everything below 50Hz
			filterChain = "lowpass=f=50,astats=metadata=1:reset=1"
			
		case "highpass":
			// Everything above 12.8kHz
			filterChain = "highpass=f=12800,astats=metadata=1:reset=1"
			
		case "bandpass":
			// Extract center frequency and calculate bandwidth
			centerFreq, bandwidth := n.getBandpassParams(band.Frequency)
			filterChain = fmt.Sprintf("bandpass=f=%d:width_type=o:width=1,astats=metadata=1:reset=1", centerFreq)
			n.logToFile(n.logFile, fmt.Sprintf("Band %s: center=%dHz, bandwidth=%.1fHz (1 octave)", 
				band.Frequency, centerFreq, bandwidth))
		}

		n.logStatus(fmt.Sprintf("  Measuring %s band...", band.Frequency))
		
		cmd := exec.Command(
			ffmpegPath,
			"-i", inputPath,
			"-af", filterChain,
			"-f", "null",
			"-",
		)
		hideWindow(cmd)

		output, err := cmd.CombinedOutput()
		if err != nil {
			n.logStatus(fmt.Sprintf("    Failed to analyze %s: %v", band.Frequency, err))
			n.logToFile(n.logFile, fmt.Sprintf("Failed %s analysis: %v", band.Frequency, err))
			continue
		}

		// Log raw FFmpeg output for debugging
		n.logToFile(n.logFile, fmt.Sprintf("=== RAW OUTPUT for %s ===", band.Frequency))
		n.logToFile(n.logFile, string(output))
		n.logToFile(n.logFile, fmt.Sprintf("=== END RAW OUTPUT for %s ===", band.Frequency))

		// Parse astats output for this band
		stats := n.parseFrequencyBandStats(string(output))
		band.RMSLevel = stats["rms"]
		band.PeakLevel = stats["peak"]
		band.CrestFactor = stats["crest"]

		n.logStatus(fmt.Sprintf("    %s: RMS=%.1f dB, Peak=%.1f dB, Crest=%.1f dB", 
			band.Frequency, band.RMSLevel, band.PeakLevel, band.CrestFactor))
		n.logToFile(n.logFile, fmt.Sprintf("%s - RMS: %.2f dB, Peak: %.2f dB, Crest: %.2f dB",
			band.Frequency, band.RMSLevel, band.PeakLevel, band.CrestFactor))
	}

	n.logStatus("Frequency response analysis complete")
	n.logToFile(n.logFile, "Frequency response analysis finished")
	
	return bands
}

// getBandpassParams returns center frequency and bandwidth in Hz for bandpass analysis
func (n *AudioNormalizer) getBandpassParams(freqStr string) (int, float64) {
	// Map frequency strings to actual Hz values
	freqMap := map[string]int{
		"100Hz":   100,
		"200Hz":   200,
		"400Hz":   400,
		"800Hz":   800,
		"1.6kHz":  1600,
		"3.2kHz":  3200,
		"6.4kHz":  6400,
		"12.8kHz": 12800,
	}
	
	centerFreq := freqMap[freqStr]
	
	// 1 octave bandwidth means bandwidth = centerFreq (from lower -1/2 octave to upper +1/2 octave)
	// But for bandpass filter with width_type=o (octave), we specify width=1 for 1 octave
	bandwidth := float64(centerFreq) // Full octave bandwidth in Hz
	
	return centerFreq, bandwidth
}

// parseFrequencyBandStats extracts RMS, peak, and crest factor from astats output
func (n *AudioNormalizer) parseFrequencyBandStats(output string) map[string]float64 {
	stats := make(map[string]float64)
	
	// Parse RMS level (dB)
	// Example: "RMS level dB: -23.45"
	rmsRe := regexp.MustCompile(`RMS level dB:\s+([-\d.]+)`)
	if match := rmsRe.FindStringSubmatch(output); len(match) > 1 {
		if val, err := strconv.ParseFloat(match[1], 64); err == nil {
			stats["rms"] = val
		}
	}
	
	// Parse Peak level (dB)
	// Example: "Peak level dB: -12.34"
	peakRe := regexp.MustCompile(`Peak level dB:\s+([-\d.]+)`)
	if match := peakRe.FindStringSubmatch(output); len(match) > 1 {
		if val, err := strconv.ParseFloat(match[1], 64); err == nil {
			stats["peak"] = val
		}
	}
	
	// Parse Crest factor (ratio, not dB)
	// Example: "Crest factor: 2.858335"
	crestRe := regexp.MustCompile(`Crest factor:\s+([-\d.]+)`)
	if match := crestRe.FindStringSubmatch(output); len(match) > 1 {
		if val, err := strconv.ParseFloat(match[1], 64); err == nil {
			stats["crest"] = val
		}
	}
	
	return stats
}

// buildEqFilter creates an EQ filter chain based on frequency response analysis
func (n *AudioNormalizer) buildEqFilter(bands []FrequencyBand, eqTarget string) string {
	if len(bands) == 0 || eqTarget == "Off" {
		return ""
	}
	
	n.logToFile(n.logFile, fmt.Sprintf("Building EQ filter for target: %s", eqTarget))
	
	// Calculate target curve
	targetLevels := n.calculateTargetCurve(bands, eqTarget)
	
	// Build filter chain using bass/highshelf for extremes and anequalizer for middle
	var filterParts []string
	
	for i, band := range bands {
		targetLevel := targetLevels[i]
		gain := targetLevel - band.RMSLevel
		
		// Limit gain to ±10 dB to avoid excessive boost/cut
		const maxGain = 10.0
		if gain > maxGain {
			n.logToFile(n.logFile, fmt.Sprintf("  %s: calculated gain %.2f dB limited to +%.1f dB", band.Frequency, gain, maxGain))
			gain = maxGain
		} else if gain < -maxGain {
			n.logToFile(n.logFile, fmt.Sprintf("  %s: calculated gain %.2f dB limited to -%.1f dB", band.Frequency, gain, maxGain))
			gain = -maxGain
		}
		
		// Skip if adjustment is tiny (< 0.5 dB)
		if gain > -0.5 && gain < 0.5 {
			n.logToFile(n.logFile, fmt.Sprintf("  %s: no adjustment needed (%.2f dB)", band.Frequency, gain))
			continue
		}
		
		n.logToFile(n.logFile, fmt.Sprintf("  %s: RMS=%.2f dB, Target=%.2f dB, Gain=%.2f dB", 
			band.Frequency, band.RMSLevel, targetLevel, gain))
		
		// Build filter based on band type
		switch band.FilterType {
		case "lowpass":
			// Use lowshelf filter for sub-50Hz
			filterParts = append(filterParts, fmt.Sprintf("lowshelf=f=50:g=%.2f:width_type=q:width=0.7", gain))
			
		case "highpass":
			// Use highshelf filter for 12.8kHz+
			filterParts = append(filterParts, fmt.Sprintf("highshelf=f=12800:g=%.2f:width_type=q:width=0.7", gain))
			
		case "bandpass":
			// Use anequalizer for middle bands
			centerFreq, bandwidth := n.getBandpassParams(band.Frequency)
			// anequalizer width is in Hz, not Q
			// For 1 octave: bandwidth = centerFreq (from -1/2 octave to +1/2 octave)
			// Apply to both channels: c0 (left) and c1 (right)
			filterParts = append(filterParts, fmt.Sprintf("anequalizer=c0 f=%d w=%.0f g=%.2f t=0|c1 f=%d w=%.0f g=%.2f t=0", 
				centerFreq, bandwidth, gain, centerFreq, bandwidth, gain))
		}
	}
	
	if len(filterParts) == 0 {
		n.logToFile(n.logFile, "No EQ adjustments needed")
		return ""
	}
	
	// Join all filter parts with commas
	eqChain := strings.Join(filterParts, ",")
	n.logToFile(n.logFile, fmt.Sprintf("Final EQ chain: %s", eqChain))
	
	return eqChain
}

// calculateTargetCurve determines target RMS levels for each band based on EQ target
func (n *AudioNormalizer) calculateTargetCurve(bands []FrequencyBand, eqTarget string) []float64 {
	targets := make([]float64, len(bands))
	
	// Calculate overall average RMS across all bands
	var overallRMS float64
	for _, band := range bands {
		overallRMS += band.RMSLevel
	}
	overallRMS = overallRMS / float64(len(bands))
	
	n.logToFile(n.logFile, fmt.Sprintf("Overall average RMS: %.2f dB", overallRMS))
	
	switch eqTarget {
	case "Flat":
		// Flat: Attenuate anything above pink noise curve
		// Pink noise: -3 dB per octave rise (reference at 1kHz)
		// Use overall RMS as base, adjust per octave from 1kHz
		
		for i, band := range bands {
			// Calculate pink noise reference level for this band
			octavesFrom1k := n.getOctavesFrom1k(band.Frequency)
			pinkNoiseRef := overallRMS + (octavesFrom1k * 3.0) // +3 dB per octave down from 1k
			
			// If measured level exceeds reference, attenuate
			if band.RMSLevel > pinkNoiseRef {
				excess := band.RMSLevel - pinkNoiseRef
				// Apply 2:1 ratio
				attenuation := excess / 2.0
				// Limit to -10 dB max
				if attenuation > 10.0 {
					attenuation = 10.0
				}
				targets[i] = band.RMSLevel - attenuation
				n.logToFile(n.logFile, fmt.Sprintf("  %s: %.2f dB exceeds pink curve (%.2f dB) by %.2f dB, attenuate %.2f dB", 
					band.Frequency, band.RMSLevel, pinkNoiseRef, excess, attenuation))
			} else {
				// Below curve, leave it alone
				targets[i] = band.RMSLevel
			}
		}
		
	case "Speech":
		// Speech: More aggressive cuts on problem frequencies
		// Target 250Hz, 400Hz down by 3 dB below pink curve
		// Target 800Hz, 3.2kHz down by 2 dB below pink curve
		
		for i, band := range bands {
			octavesFrom1k := n.getOctavesFrom1k(band.Frequency)
			pinkNoiseRef := overallRMS + (octavesFrom1k * 3.0)
			
			// Apply specific offsets for speech clarity
			var targetOffset float64
			switch band.Frequency {
			case "50Hz", "100Hz":
				targetOffset = -4.0 // Cut sub-bass/bass rumble
			case "200Hz":
				targetOffset = -3.0 // Cut mud
			case "400Hz":
				targetOffset = -3.0 // Cut boxiness
			case "800Hz":
				targetOffset = -2.0 // Slight cut
			case "3.2kHz":
				targetOffset = -2.0 // Control sibilance
			default:
				targetOffset = 0.0
			}
			
			speechTarget := pinkNoiseRef + targetOffset
			
			if band.RMSLevel > speechTarget {
				excess := band.RMSLevel - speechTarget
				attenuation := excess / 2.0 // 2:1 ratio
				if attenuation > 10.0 {
					attenuation = 10.0
				}
				targets[i] = band.RMSLevel - attenuation
				n.logToFile(n.logFile, fmt.Sprintf("  %s: %.2f dB exceeds speech target (%.2f dB) by %.2f dB, attenuate %.2f dB", 
					band.Frequency, band.RMSLevel, speechTarget, excess, attenuation))
			} else {
				targets[i] = band.RMSLevel
			}
		}
		
	case "Broadcast":
		// Broadcast: Flat cuts + gentle Fletcher-Munson compensation on extremes
		
		for i, band := range bands {
			octavesFrom1k := n.getOctavesFrom1k(band.Frequency)
			pinkNoiseRef := overallRMS + (octavesFrom1k * 3.0)
			
			// Apply broadcast-specific offsets
			var targetOffset float64
			switch band.Frequency {
			case "50Hz":
				targetOffset = -3.0 // Cut sub-bass
			case "100Hz", "200Hz":
				targetOffset = -2.0 // Cut bass
			case "400Hz":
				targetOffset = -3.0 // Cut boxiness
			case "800Hz":
				targetOffset = -2.0
			case "3.2kHz":
				targetOffset = -2.0
			case "12.8kHz", "12.8kHz+":
				// Fletcher-Munson: gentle boost on highs (but implemented as less attenuation)
				targetOffset = +1.0 // Allow 1 dB above curve
			default:
				targetOffset = 0.0
			}
			
			broadcastTarget := pinkNoiseRef + targetOffset
			
			if band.RMSLevel > broadcastTarget {
				excess := band.RMSLevel - broadcastTarget
				attenuation := excess / 2.0
				if attenuation > 10.0 {
					attenuation = 10.0
				}
				targets[i] = band.RMSLevel - attenuation
				n.logToFile(n.logFile, fmt.Sprintf("  %s: %.2f dB exceeds broadcast target (%.2f dB) by %.2f dB, attenuate %.2f dB", 
					band.Frequency, band.RMSLevel, broadcastTarget, excess, attenuation))
			} else {
				targets[i] = band.RMSLevel
			}
		}
		
	default:
		// No EQ
		for i, band := range bands {
			targets[i] = band.RMSLevel
		}
	}
	
	return targets
}

// getOctavesFrom1k returns the number of octaves from 1kHz for a given frequency band
func (n *AudioNormalizer) getOctavesFrom1k(freqStr string) float64 {
	// Reference: 1kHz = 0 octaves
	// Formula: octaves = log2(freq / 1000)
	switch freqStr {
	case "50Hz":
		return -4.32  // log2(50/1000)
	case "100Hz":
		return -3.32  // log2(100/1000)
	case "200Hz":
		return -2.32  // log2(200/1000)
	case "400Hz":
		return -1.32  // log2(400/1000)
	case "800Hz":
		return -0.32  // log2(800/1000)
	case "1.6kHz":
		return 0.68   // log2(1600/1000)
	case "3.2kHz":
		return 1.68   // log2(3200/1000)
	case "6.4kHz":
		return 2.68   // log2(6400/1000)
	case "12.8kHz":
		return 3.68   // log2(12800/1000)
	case "12.8kHz+":
		return 4.5    // Approximate for >12.8kHz
	default:
		return 0.0
	}
}