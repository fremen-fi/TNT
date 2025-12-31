package audio

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ParseAstatsOutput parses FFmpeg astats filter output into DynamicsAnalysis
func ParseAstatsOutput(output string) *DynamicsAnalysis {
	result := &DynamicsAnalysis{}

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

// ParseDynamicsScore parses astats output for Dynamics Score calculation
func ParseDynamicsScore(output string) *DynamicsScoreAnalysis {
	result := &DynamicsScoreAnalysis{}

	// Parse Channel 1 section
	lines := strings.Split(output, "\n")
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

	// Calculate DS = sqrt(Crest) × (RMS_peak - RMS_level)
	result.DynamicsScore = math.Sqrt(result.CrestFactor) * (result.RMSPeak - result.RMSLevel)

	return result
}

// ParseFrequencyBandOutput parses astats output for a single frequency band
func ParseFrequencyBandOutput(output string, bandName string) *FrequencyBandAnalysis {
	result := &FrequencyBandAnalysis{BandName: bandName}

	// Find Overall section
	overallStart := strings.Index(output, "Overall")
	if overallStart == -1 {
		return nil
	}
	overallSection := output[overallStart:]

	// Parse: Peak level dB
	peakRe := regexp.MustCompile(`Peak level dB:\s+([-\d.]+)`)
	if match := peakRe.FindStringSubmatch(overallSection); len(match) > 1 {
		result.PeakLevel, _ = strconv.ParseFloat(match[1], 64)
	}

	// Parse: RMS level dB
	rmsRe := regexp.MustCompile(`RMS level dB:\s+([-\d.]+)`)
	if match := rmsRe.FindStringSubmatch(overallSection); len(match) > 1 {
		result.RMSLevel, _ = strconv.ParseFloat(match[1], 64)
	}

	// Parse: Crest factor
	crestRe := regexp.MustCompile(`Crest factor:\s+([-\d.]+)`)
	if match := crestRe.FindStringSubmatch(overallSection); len(match) > 1 {
		result.CrestFactor, _ = strconv.ParseFloat(match[1], 64)
	}

	// Parse: Dynamic range
	dynRe := regexp.MustCompile(`Dynamic range:\s+([-\d.]+)`)
	if match := dynRe.FindStringSubmatch(overallSection); len(match) > 1 {
		result.DynamicRange, _ = strconv.ParseFloat(match[1], 64)
	}

	return result
}

// FrequencyBandFilters returns the filter strings for each frequency band
func FrequencyBandFilters() map[string]string {
	return map[string]string{
		"sub":     "lowpass=f=80",
		"bass":    "highpass=f=80,lowpass=f=250",
		"low_mid": "highpass=f=250,lowpass=f=1000",
		"mid":     "highpass=f=1000,lowpass=f=4000",
		"high":    "highpass=f=4000",
	}
}
