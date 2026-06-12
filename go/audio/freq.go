package audio

import (
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// AnalyzeFrequencyResponseBands analyzes the frequency response across 10
// bands using lowpass, bandpass, and highpass filters with astats. Each
// band is an independent ffmpeg invocation reading the source file; they
// run concurrently up to min(numBands, GOMAXPROCS) goroutines to use
// available cores. A single failed band stops the analysis and returns
// the first error.
func AnalyzeFrequencyResponseBands(ffmpegPath, inputPath string) ([]FrequencyBand, error) {
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

	// Bound concurrency at GOMAXPROCS to avoid swamping the box on small
	// hosts; 10 ffmpeg processes on a 4-core machine is worse than 4.
	maxParallel := runtime.GOMAXPROCS(0)
	if maxParallel < 1 {
		maxParallel = 1
	}
	if maxParallel > len(bands) {
		maxParallel = len(bands)
	}

	sem := make(chan struct{}, maxParallel)
	errs := make([]error, len(bands))
	var wg sync.WaitGroup

	for i := range bands {
		i := i
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

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
				centerFreq, _ := getBandpassParams(band.Frequency)
				filterChain = fmt.Sprintf("bandpass=f=%d:width_type=o:width=1,astats", centerFreq)
			}

			cmd := exec.Command(
				ffmpegPath,
				"-i", inputPath,
				"-af", filterChain,
				"-f", "null",
				"-",
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				errs[i] = fmt.Errorf("ffmpeg analysis of %s band failed: %w", band.Frequency, err)
				return
			}
			stats := parseFrequencyBandStats(string(out))
			band.RMSLevel = stats["rms"]
			band.PeakLevel = stats["peak"]
			band.CrestFactor = stats["crest"]
		}()
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return bands, nil
}

// getBandpassParams returns the center frequency (Hz) and 1-octave bandwidth
// (Hz) for a named bandpass band.
func getBandpassParams(freqStr string) (int, float64) {
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

// parseFrequencyBandStats extracts RMS, peak, and crest factor from astats output.
func parseFrequencyBandStats(output string) map[string]float64 {
	stats := make(map[string]float64)

	// Parse RMS level (dB), e.g. "RMS level dB: -23.45"
	rmsRe := regexp.MustCompile(`RMS level dB:\s+([-\d.]+)`)
	if match := rmsRe.FindStringSubmatch(output); len(match) > 1 {
		if val, err := strconv.ParseFloat(match[1], 64); err == nil {
			stats["rms"] = val
		}
	}

	// Parse Peak level (dB), e.g. "Peak level dB: -12.34"
	peakRe := regexp.MustCompile(`Peak level dB:\s+([-\d.]+)`)
	if match := peakRe.FindStringSubmatch(output); len(match) > 1 {
		if val, err := strconv.ParseFloat(match[1], 64); err == nil {
			stats["peak"] = val
		}
	}

	// Parse Crest factor (ratio, not dB), e.g. "Crest factor: 2.858335"
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
//  1. Extreme-to-extreme difference <= 4 dB
//  2. Extreme-to-neighbor difference <= 4 dB
//  3. Neither extreme exceeds the average of non-extremes in magnitude
func clampExtremeEQ(gains []float64) []float64 {
	if len(gains) < 3 {
		return gains // Need at least 3 bands (2 extremes + 1 middle)
	}

	clamped := make([]float64, len(gains))
	copy(clamped, gains)

	lowShelf := gains[0]             // First band (sub 100Hz)
	highShelf := gains[len(gains)-1] // Last band (above 12.8kHz)

	// Calculate average of non-extremes
	var sum float64
	for i := 1; i < len(gains)-1; i++ {
		sum += gains[i]
	}
	avgNonExtremes := sum / float64(len(gains)-2)

	// First neighbor (band index 1)
	firstNeighbor := gains[1]
	// Last neighbor (band index len-2)
	lastNeighbor := gains[len(gains)-2]

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

	// Take most restrictive limits
	maxLow := math.Min(math.Min(maxLowFromHigh, maxLowFromNeighbor), maxLowFromAvg)
	minLow := math.Max(math.Max(minLowFromHigh, minLowFromNeighbor), minLowFromAvg)

	if lowShelf > maxLow {
		clamped[0] = maxLow
	} else if lowShelf < minLow {
		clamped[0] = minLow
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

	maxHigh := math.Min(math.Min(maxHighFromLow, maxHighFromNeighbor), maxHighFromAvg)
	minHigh := math.Max(math.Max(minHighFromLow, minHighFromNeighbor), minHighFromAvg)

	if highShelf > maxHigh {
		clamped[len(clamped)-1] = maxHigh
	} else if highShelf < minHigh {
		clamped[len(clamped)-1] = minHigh
	}

	return clamped
}

// BuildEqFilter creates an ffmpeg EQ filter chain based on frequency response
// analysis for the given EQ target ("Flat", "Speech", "Broadcast", or "Off").
// Returns an empty string when no adjustment is needed.
func BuildEqFilter(bands []FrequencyBand, eqTarget string) string {
	if len(bands) == 0 || eqTarget == "Off" {
		return ""
	}

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
	targetLevels := CalculateTargetCurve(bands, eqTarget)

	// Build filter chain using bass/highshelf for extremes and anequalizer for middle
	var filterParts []string

	// Collect all gains first
	gains := make([]float64, len(bands))

	for i, band := range bands {
		targetLevel := targetLevels[i]
		gain := targetLevel - band.RMSLevel

		const maxGain = 10.0
		if gain > maxGain {
			gain = maxGain
		} else if gain < -maxGain {
			gain = -maxGain
		}

		gains[i] = gain
	}

	// Apply extreme band constraints
	gains = clampExtremeEQ(gains)

	// Build filters using clamped gains
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
			centerFreq, bandwidth := getBandpassParams(band.Frequency)
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
		return ""
	}

	// Join all filter parts with commas
	return strings.Join(finalParts, ",")
}

// CalculateTargetCurve determines target RMS levels (dB) for each band based on
// the EQ target ("Flat", "Speech", "Broadcast"; any other value yields the
// measured levels unchanged).
func CalculateTargetCurve(bands []FrequencyBand, eqTarget string) []float64 {
	targets := make([]float64, len(bands))

	// Calculate overall average RMS across all bands
	var overallRMS float64
	for _, band := range bands {
		overallRMS += band.RMSLevel
	}
	overallRMS = overallRMS / float64(len(bands))

	switch eqTarget {
	case "Flat":
		// Flat: Attenuate anything above pink noise curve
		// Pink noise: -3 dB per octave rise (reference at 1kHz)
		// Use overall RMS as base, adjust per octave from 1kHz

		for i, band := range bands {
			// Calculate pink noise reference level for this band
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
			} else {
				// Below curve, leave it alone
				targets[i] = band.RMSLevel
			}
		}

	case "Speech":
		// Speech: Optimize for intelligibility (relative to pink noise curve)

		for i, band := range bands {
			octavesFrom1k := getOctavesFrom1k(band.Frequency)
			pinkNoiseRef := overallRMS - (octavesFrom1k * 3.0)

			// Target adjustments relative to pink noise
			var adjustment float64
			switch band.Frequency {
			case "50Hz":
				adjustment = -9.0
			case "100Hz":
				adjustment = -3.5
			case "200Hz":
				adjustment = -2.5
			case "400Hz":
				adjustment = -3.0
			case "800Hz":
				adjustment = +0.5
			case "1.6kHz":
				adjustment = +3.0
			case "3.2kHz":
				adjustment = +1.0
			case "6.4kHz":
				adjustment = +0.0
			case "12.8kHz":
				adjustment = -2.0
			case "12.8kHz+":
				adjustment = -2.0
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
		}

	case "Broadcast":
		// Broadcast: Aggressive clarity for small speakers/phones (relative to
		// pink noise curve)

		for i, band := range bands {
			octavesFrom1k := getOctavesFrom1k(band.Frequency)
			pinkNoiseRef := overallRMS - (octavesFrom1k * 3.0)

			// Target adjustments relative to pink noise
			var adjustment float64
			switch band.Frequency {
			case "50Hz":
				adjustment = -2.0
			case "100Hz":
				adjustment = -1.0
			case "200Hz":
				adjustment = -2.5
			case "400Hz":
				adjustment = -4.5
			case "800Hz":
				adjustment = +1.0
			case "1.6kHz":
				adjustment = +2.5
			case "3.2kHz":
				adjustment = +3.5
			case "6.4kHz":
				adjustment = +2.0
			case "12.8kHz":
				adjustment = -0.5
			case "12.8kHz+":
				adjustment = -2.5
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
		}

	default:
		// No EQ
		for i, band := range bands {
			targets[i] = band.RMSLevel
		}
	}

	return targets
}

// getOctavesFrom1k returns the number of octaves from 1kHz for a given
// frequency band.
func getOctavesFrom1k(freqStr string) float64 {
	// Reference: 1kHz = 0 octaves; octaves = log2(freq / 1000)
	switch freqStr {
	case "50Hz":
		return -4.32 // log2(50/1000)
	case "100Hz":
		return -3.32 // log2(100/1000)
	case "200Hz":
		return -2.32 // log2(200/1000)
	case "400Hz":
		return -1.32 // log2(400/1000)
	case "800Hz":
		return -0.32 // log2(800/1000)
	case "1.6kHz":
		return 0.68 // log2(1600/1000)
	case "3.2kHz":
		return 1.68 // log2(3200/1000)
	case "6.4kHz":
		return 2.68 // log2(6400/1000)
	case "12.8kHz":
		return 3.68 // log2(12800/1000)
	case "12.8kHz+":
		return 5.0 // Approximate for >12.8kHz
	default:
		return 0.0
	}
}
