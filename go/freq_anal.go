package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"math"
	"github.com/fremen-fi/tnt/go/internal/ffmpeg"
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
			filterChain = "highpass=f=25:p=1:r=f64:p=2,lowpass=f=50,astats"
			
		case "highpass":
			// Everything above 12.8kHz
			filterChain = "highpass=f=12800,astats"
			
		case "bandpass":
			// Extract center frequency and calculate bandwidth
			centerFreq, bandwidth := n.getBandpassParams(band.Frequency)
			filterChain = fmt.Sprintf("bandpass=f=%d:width_type=o:width=1,astats", centerFreq)
			n.logToFile(n.logFile, fmt.Sprintf("Band %s: center=%dHz, bandwidth=%.1fHz (1 octave)", 
				band.Frequency, centerFreq, bandwidth))
		}

		n.logStatus(fmt.Sprintf("  Measuring %s band...", band.Frequency))
		
		cmd := ffmpeg.Command(
			
			"-i", inputPath,
			"-af", filterChain,
			"-f", "null",
			"-",
		)
		

		output, err := cmd.CombinedOutput()
		if err != nil {
			n.logStatus(fmt.Sprintf("    Failed to analyze %s: %v", band.Frequency, err))
			n.logToFile(n.logFile, fmt.Sprintf("Failed %s analysis: %v", band.Frequency, err))
			continue
		}

		// Log raw FFmpeg output for debugging
		//n.logToFile(n.logFile, fmt.Sprintf("=== RAW OUTPUT for %s ===", band.Frequency))
		//n.logToFile(n.logFile, string(output))
		//n.logToFile(n.logFile, fmt.Sprintf("=== END RAW OUTPUT for %s ===", band.Frequency))

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

// clampExtremeEQ applies restrictions to low-shelf and high-shelf EQ values
// based on the non-extreme band values:
// 1. Extreme-to-extreme difference <= 4 dB
// 2. Extreme-to-neighbor difference <= 4 dB  
// 3. Neither extreme exceeds the average of non-extremes in magnitude
func clampExtremeEQ(gains []float64, n *AudioNormalizer) []float64 {
	if len(gains) < 3 {
		n.logToFile(n.logFile, "clampExtremeEQ: Not enough bands to clamp (need at least 3)")
		return gains // Need at least 3 bands (2 extremes + 1 middle)
	}
	
	clamped := make([]float64, len(gains))
	copy(clamped, gains)
	
	lowShelf := gains[0]           // First band (sub 100Hz)
	highShelf := gains[len(gains)-1] // Last band (above 12.8kHz)
	
	n.logToFile(n.logFile, fmt.Sprintf("clampExtremeEQ: Original low extreme: %.2f dB, high extreme: %.2f dB", lowShelf, highShelf))
	
	// Calculate average of non-extremes
	var sum float64
	for i := 1; i < len(gains)-1; i++ {
		sum += gains[i]
	}
	avgNonExtremes := sum / float64(len(gains)-2)
	n.logToFile(n.logFile, fmt.Sprintf("clampExtremeEQ: Non-extremes average: %.2f dB", avgNonExtremes))
	
	// First neighbor (band index 1)
	firstNeighbor := gains[1]
	// Last neighbor (band index len-2)
	lastNeighbor := gains[len(gains)-2]
	n.logToFile(n.logFile, fmt.Sprintf("clampExtremeEQ: First neighbor: %.2f dB, Last neighbor: %.2f dB", firstNeighbor, lastNeighbor))
	
	// Clamp low shelf
	// Rule 1: Extreme-to-extreme
	maxLowFromHigh := highShelf + 4.0
	minLowFromHigh := highShelf - 4.0
	
	// Rule 2: Extreme-to-neighbor
	maxLowFromNeighbor := firstNeighbor + 4.0
	minLowFromNeighbor := firstNeighbor - 4.0
	
	// Rule 3: Extreme-to-average
	var maxLowFromAvg, minLowFromAvg float64
	if avgNonExtremes >= 0 {
		maxLowFromAvg = avgNonExtremes
		minLowFromAvg = -999.0 // No lower limit when avg is positive
	} else {
		maxLowFromAvg = 999.0 // No upper limit when avg is negative
		minLowFromAvg = avgNonExtremes
	}
	
	n.logToFile(n.logFile, fmt.Sprintf("clampExtremeEQ LOW: maxFromHigh=%.2f, minFromHigh=%.2f, maxFromNeighbor=%.2f, minFromNeighbor=%.2f, maxFromAvg=%.2f, minFromAvg=%.2f",
		maxLowFromHigh, minLowFromHigh, maxLowFromNeighbor, minLowFromNeighbor, maxLowFromAvg, minLowFromAvg))
	
	// Take most restrictive limits
	maxLow := math.Min(math.Min(maxLowFromHigh, maxLowFromNeighbor), maxLowFromAvg)
	minLow := math.Max(math.Max(minLowFromHigh, minLowFromNeighbor), minLowFromAvg)
	
	n.logToFile(n.logFile, fmt.Sprintf("clampExtremeEQ LOW: Final range [%.2f, %.2f]", minLow, maxLow))
	
	if lowShelf > maxLow {
		clamped[0] = maxLow
		n.logToFile(n.logFile, fmt.Sprintf("clampExtremeEQ LOW: CLAMPED from %.2f to %.2f (exceeded max)", lowShelf, maxLow))
	} else if lowShelf < minLow {
		clamped[0] = minLow
		n.logToFile(n.logFile, fmt.Sprintf("clampExtremeEQ LOW: CLAMPED from %.2f to %.2f (below min)", lowShelf, minLow))
	} else {
		n.logToFile(n.logFile, fmt.Sprintf("clampExtremeEQ LOW: No clamping needed (%.2f within range)", lowShelf))
	}
	
	// Clamp high shelf (same logic, inverted)
	maxHighFromLow := clamped[0] + 4.0 // Use clamped low value
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
	
	n.logToFile(n.logFile, fmt.Sprintf("clampExtremeEQ HIGH: maxFromLow=%.2f, minFromLow=%.2f, maxFromNeighbor=%.2f, minFromNeighbor=%.2f, maxFromAvg=%.2f, minFromAvg=%.2f",
		maxHighFromLow, minHighFromLow, maxHighFromNeighbor, minHighFromNeighbor, maxHighFromAvg, minHighFromAvg))
	
	maxHigh := math.Min(math.Min(maxHighFromLow, maxHighFromNeighbor), maxHighFromAvg)
	minHigh := math.Max(math.Max(minHighFromLow, minHighFromNeighbor), minHighFromAvg)
	
	n.logToFile(n.logFile, fmt.Sprintf("clampExtremeEQ HIGH: Final range [%.2f, %.2f]", minHigh, maxHigh))
	
	if highShelf > maxHigh {
		clamped[len(clamped)-1] = maxHigh
		n.logToFile(n.logFile, fmt.Sprintf("clampExtremeEQ HIGH: CLAMPED from %.2f to %.2f (exceeded max)", highShelf, maxHigh))
	} else if highShelf < minHigh {
		clamped[len(clamped)-1] = minHigh
		n.logToFile(n.logFile, fmt.Sprintf("clampExtremeEQ HIGH: CLAMPED from %.2f to %.2f (below min)", highShelf, minHigh))
	} else {
		n.logToFile(n.logFile, fmt.Sprintf("clampExtremeEQ HIGH: No clamping needed (%.2f within range)", highShelf))
	}
	
	return clamped
}

// buildEqFilter creates an EQ filter chain based on frequency response analysis
func (n *AudioNormalizer) buildEqFilter(bands []FrequencyBand, eqTarget string) string {
	if len(bands) == 0 || eqTarget == "Off" {
		return ""
	}
	
	n.logToFile(n.logFile, fmt.Sprintf("Building EQ filter for target: %s", eqTarget))
	
	// Define high-pass and low-pass filters per preset
	var highpassFilter, lowpassFilter string
	
	switch eqTarget {
	case "Flat":
		highpassFilter = "highpass=f=25:p=2"
		lowpassFilter = "" // No lowpass for Flat
	case "Speech":
		highpassFilter = "highpass=f=80:p=2"
		lowpassFilter = "lowpass=f=13000:p=1"
	case "Broadcast":
		highpassFilter = "highpass=f=70:p=2"
		lowpassFilter = "lowpass=f=14000:p=2"
	default:
		highpassFilter = ""
		lowpassFilter = ""
	}
	
	// Calculate target curve
	targetLevels := n.calculateTargetCurve(bands, eqTarget)
	
	// Build filter chain using bass/highshelf for extremes and anequalizer for middle
	var filterParts []string
	
	// Collect all gains first
	gains := make([]float64, len(bands))
	
	for i, band := range bands {
		targetLevel := targetLevels[i]
		gain := targetLevel - band.RMSLevel
		
		const maxGain = 10.0
		if gain > maxGain {
			n.logToFile(n.logFile, fmt.Sprintf("  %s: calculated gain %.2f dB limited to +%.1f dB", band.Frequency, gain, maxGain))
			gain = maxGain
		} else if gain < -maxGain {
			n.logToFile(n.logFile, fmt.Sprintf("  %s: calculated gain %.2f dB limited to -%.1f dB", band.Frequency, gain, maxGain))
			gain = -maxGain
		}
		
		gains[i] = gain
	}
	
	// Apply extreme band constraints
	gains = clampExtremeEQ(gains, n)
	
	// Build filters using clamped gains
	for i, band := range bands {
		gain := gains[i]
		
		if gain > -0.5 && gain < 0.5 {
			n.logToFile(n.logFile, fmt.Sprintf("  %s: no adjustment needed (%.2f dB)", band.Frequency, gain))
			continue
		}
		
		n.logToFile(n.logFile, fmt.Sprintf("  %s: RMS=%.2f dB, Target=%.2f dB, Gain=%.2f dB", 
			band.Frequency, band.RMSLevel, targetLevels[i], gain))
		
		switch band.FilterType {
		case "lowpass":
			filterParts = append(filterParts, fmt.Sprintf("highpass=f=25:p=1:r=f64:p=2,lowshelf=f=50:g=%.2f:width_type=q:width=0.7", gain))
		case "highpass":
			filterParts = append(filterParts, fmt.Sprintf("lowpass=f=17500:p=2:r=f64,highshelf=f=12800:g=%.2f:width_type=q:width=0.7", gain))
		case "bandpass":
			centerFreq, bandwidth := n.getBandpassParams(band.Frequency)
			filterParts = append(filterParts, fmt.Sprintf("anequalizer=c0 f=%d w=%.0f g=%.2f t=0|c1 f=%d w=%.0f g=%.2f t=0", 
				centerFreq, bandwidth, gain, centerFreq, bandwidth, gain))
		}
	}
	
	// Build final chain with HPF, EQ bands, LPF
	var finalParts []string
	
	if highpassFilter != "" {
		finalParts = append(finalParts, highpassFilter)
	}
	
	finalParts = append(finalParts, filterParts...)
	
	if lowpassFilter != "" {
		finalParts = append(finalParts, lowpassFilter)
	}
	
	if len(finalParts) == 0 {
		n.logToFile(n.logFile, "No EQ adjustments needed")
		return ""
	}
	
	// Join all filter parts with commas
	eqChain := strings.Join(finalParts, ",")

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
			// Pink noise: +3 dB per octave down from 1kHz (more energy in bass)
			pinkNoiseRef := overallRMS
			
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
		// Speech: Optimize for intelligibility
		// Reference: Gemini tables (relative to pink noise curve)
		
		for i, band := range bands {
			octavesFrom1k := n.getOctavesFrom1k(band.Frequency)
			pinkNoiseRef := overallRMS - (octavesFrom1k * 3.0)
			
			// Target adjustments relative to pink noise
			var adjustment float64
			switch band.Frequency {
			case "50Hz":
				adjustment = -9.0  // -6 to -12 dB cut (using -9 dB)
			case "100Hz":
				adjustment = -3.5  // -3 to -6 dB cut (using -4.5 dB)
			case "200Hz":
				adjustment = -2.5  // 0 to +2 dB (using +1 dB for slight warmth)
			case "400Hz":
				adjustment = -3.0  // -3 to -5 dB cut (reduce boxiness)
			case "800Hz":
				adjustment = +0.5  // 0 to +1 dB (slight boost for projection)
			case "1.6kHz":
				adjustment = +3.0  // +2 to +4 dB boost (intelligibility core)
			case "3.2kHz":
				adjustment = +1.0  // +1 to +3 dB boost (presence)
			case "6.4kHz":
				adjustment = +0.0  // 0 to +2 dB (add air)
			case "12.8kHz":
				adjustment = -2.0  // 0 to +3 dB (add openness)
			case "12.8kHz+":
				adjustment = -2.0   // Flat or slight high-pass
			default:
				adjustment = 0.0
			}
			
			targetLevel := pinkNoiseRef + adjustment
			deviation := band.RMSLevel - targetLevel
			
			// Apply 2:1 ratio
			correction := deviation / 2.0
			
			// Limit correction to ±10 dB
			if correction > 10.0 {
				correction = 10.0
			} else if correction < -10.0 {
				correction = -10.0
			}
			
			// Skip tiny adjustments
			if correction > -0.5 && correction < 0.5 {
				targets[i] = band.RMSLevel
				continue
			}
			
			targets[i] = band.RMSLevel - correction
			
			if correction > 0 {
				n.logToFile(n.logFile, fmt.Sprintf("  %s: %.2f dB exceeds speech target (%.2f dB) by %.2f dB, attenuate %.2f dB", 
					band.Frequency, band.RMSLevel, targetLevel, deviation, correction))
			} else {
				n.logToFile(n.logFile, fmt.Sprintf("  %s: %.2f dB below speech target (%.2f dB) by %.2f dB, boost %.2f dB", 
					band.Frequency, band.RMSLevel, targetLevel, -deviation, -correction))
			}
		}
		
	case "Broadcast":
		// Broadcast: Aggressive clarity for small speakers/phones
		// Reference: Gemini tables (relative to pink noise curve)
		
		for i, band := range bands {
			octavesFrom1k := n.getOctavesFrom1k(band.Frequency)
			pinkNoiseRef := overallRMS - (octavesFrom1k * 3.0)
			
			// Target adjustments relative to pink noise
			var adjustment float64
			switch band.Frequency {
			case "50Hz":
				adjustment = -2.0  // Aggressive high-pass
			case "100Hz":
				adjustment = -1.0   // -6 to -10 dB deep cut
			case "200Hz":
				adjustment = -2.5   // -2 to +1 dB (using -0.5 for clarity)
			case "400Hz":
				adjustment = -4.5   // -4 to -7 dB (eliminate boxiness)
			case "800Hz":
				adjustment = +1.0   // +1 to +3 dB (forwardness)
			case "1.6kHz":
				adjustment = +2.5   // +3 to +6 dB (maximize intelligibility)
			case "3.2kHz":
				adjustment = +3.5   // +2 to +5 dB (presence and crispness)
			case "6.4kHz":
				adjustment = +2.0   // +2 to +4 dB (sparkle and polish)
			case "12.8kHz":
				adjustment = -0.5   // -3 to 0 dB (reduce hiss)
			case "12.8kHz+":
				adjustment = -2.5   // Slight roll-off
			default:
				adjustment = 0.0
			}
			
			targetLevel := pinkNoiseRef + adjustment
			deviation := band.RMSLevel - targetLevel
			
			// Apply 2:1 ratio
			correction := deviation / 2.0
			
			// Limit correction to ±10 dB
			if correction > 10.0 {
				correction = 10.0
			} else if correction < -10.0 {
				correction = -10.0
			}
			
			// Skip tiny adjustments
			if correction > -0.5 && correction < 0.5 {
				targets[i] = band.RMSLevel
				continue
			}
			
			targets[i] = band.RMSLevel - correction
			
			if correction > 0 {
				n.logToFile(n.logFile, fmt.Sprintf("  %s: %.2f dB exceeds broadcast target (%.2f dB) by %.2f dB, attenuate %.2f dB", 
					band.Frequency, band.RMSLevel, targetLevel, deviation, correction))
			} else {
				n.logToFile(n.logFile, fmt.Sprintf("  %s: %.2f dB below broadcast target (%.2f dB) by %.2f dB, boost %.2f dB", 
					band.Frequency, band.RMSLevel, targetLevel, -deviation, -correction))
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
		return 5.0    // Approximate for >12.8kHz
	default:
		return 0.0
	}
}