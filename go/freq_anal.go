package main

import (
	"fmt"

	"github.com/fremen-fi/tnt/go/audio"
	"github.com/fremen-fi/tnt/go/internal/ffmpeg"
)

// FrequencyBand is an alias for the public audio package type. The EQ analysis
// logic lives in github.com/fremen-fi/tnt/go/audio; these receiver methods are
// thin wrappers that add app-level logging.
type FrequencyBand = audio.FrequencyBand

// analyzeFrequencyResponseBands analyzes the frequency response across 10 bands.
func (n *AudioNormalizer) analyzeFrequencyResponseBands(inputPath string) []FrequencyBand {
	n.appLog.Write("Analyzing frequency response across 10 bands...")
	n.logFile.Write("Starting frequency response analysis")

	bands, err := audio.AnalyzeFrequencyResponseBands(ffmpeg.Path, inputPath)
	if err != nil {
		n.appLog.Write(fmt.Sprintf("Frequency response analysis failed: %v", err))
		n.logFile.Write(fmt.Sprintf("Frequency response analysis failed: %v", err))
		return nil
	}

	for _, band := range bands {
		n.logFile.Write(fmt.Sprintf("%s - RMS: %.2f dB, Peak: %.2f dB, Crest: %.2f dB",
			band.Frequency, band.RMSLevel, band.PeakLevel, band.CrestFactor))
	}

	n.appLog.Write("Frequency response analysis complete")
	n.logFile.Write("Frequency response analysis finished")

	return bands
}

// buildEqFilter creates an EQ filter chain based on frequency response analysis.
func (n *AudioNormalizer) buildEqFilter(bands []FrequencyBand, eqTarget string) string {
	eqChain := audio.BuildEqFilter(bands, eqTarget)
	if eqChain != "" {
		n.logFile.Write(fmt.Sprintf("Final EQ chain: %s", eqChain))
	}
	return eqChain
}
