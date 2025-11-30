package main

import (
	"fmt"
	"strings"
	"regexp"
	"strconv"
	"os/exec"
	"path/filepath"
	"math"
)

type DynamicsScoreAnalysis struct {
	RMSPeak       float64
	RMSLevel      float64
	CrestFactor   float64
	DynamicsScore float64
}

func (n *AudioNormalizer) calculateDynamicsScore(inputPath string) *DynamicsScoreAnalysis {
	n.logStatus(fmt.Sprintf("→ Calculating Dynamics Score: %s", filepath.Base(inputPath)))
	
	cmd := exec.Command(
		ffmpegPath,
		"-i", inputPath,
		"-af", "astats",
		"-f", "null",
		"-",
	)
	hideWindow(cmd)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		n.logToFile(n.logFile, fmt.Sprintf("DS calculation failed: %v", err))
		return nil
	}
	
	return n.parseDynamicsScore(string(output))
}

func (n *AudioNormalizer) parseDynamicsScore(output string) *DynamicsScoreAnalysis {
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
	
	// Calculate DS = Crest × (RMS_peak - RMS_level)
	result.DynamicsScore = math.Sqrt(result.CrestFactor) * (result.RMSPeak - result.RMSLevel)
	
	n.logToFile(n.logFile, fmt.Sprintf("DS Analysis - RMS Peak: %.2f dB, RMS Level: %.2f dB, Crest: %.2f", 
		result.RMSPeak, result.RMSLevel, result.CrestFactor))
	n.logToFile(n.logFile, fmt.Sprintf("Dynamics Score: %.2f", result.DynamicsScore))
	
	return result
}

type CompressionModifiers struct {
	AttackMultiplier  float64
	ReleaseMultiplier float64
	RatioMultiplier   float64  // The target ratio (1.4, 2.1, 4.0, or over 4.0 up to 8.0 etc)
}

func getCompressionModifiers(ds float64) CompressionModifiers {
	mods := CompressionModifiers{
		AttackMultiplier:  1.0,
		ReleaseMultiplier: 1.0,
		RatioMultiplier:   1.0,  // 0 = no change from preset
	}
	
	if ds < 9.0 {
		// Very compressed - slow down, barely compress
		mods.AttackMultiplier = 4.0
		mods.ReleaseMultiplier = 4.0
		mods.RatioMultiplier = 0.15
		
	} else if ds < 15.0 {
		// Moderately compressed - slow down, gentle
		mods.AttackMultiplier = 2.0
		mods.ReleaseMultiplier = 2.0
		mods.RatioMultiplier = 2.1
		
	} else if ds <= 21.0 {
		// Normal - use preset as-is
		// No changes
		
	} else {
		// Highly dynamic - speed up, more aggressive
		// Linear scaling from DS=21 to DS=100
		// At DS=21: divide by 2, ratio 4:1
		// At DS=100: divide by 4, ratio 8:1
		
		excess := math.Min(ds - 21.0, 79.0)  // Cap at 79 (21+79=100)
		scaleFactor := 2.0 + (2.0 * excess / 79.0)  // 2.0 to 4.0
		
		mods.AttackMultiplier = 1.0 / scaleFactor
		mods.ReleaseMultiplier = 1.0 / scaleFactor
		mods.RatioMultiplier = 4.0 + (4.0 * excess / 79.0)  // 4.0 to 8.0
	}
	
	return mods
}

func getBaseRatioFromCrest(crest float64) float64 {
	if crest <= 3.0 {
		return 1.4
	} else if crest <= 5.0 {
		return 2.0
	} else if crest <= 8.0 {
		return 4.0
	} else {
		// Linear scale from 4.0 to 8.0 for crest 8-16
		// Cap at 8.0 for crest >= 16
		if crest >= 16.0 {
			return 8.0
		}
		// crest is between 8 and 16
		return 4.0 + (4.0 * (crest - 8.0) / 8.0)
	}
}