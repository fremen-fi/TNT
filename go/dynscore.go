package main

import (
	"fmt"
	"path/filepath"

	"github.com/fremen-fi/tnt/go/audio"
	"github.com/fremen-fi/tnt/go/internal/ffmpeg"
)

// calculateDynamicsScore runs ffmpeg astats and parses the Dynamics Score. The
// calculation lives in github.com/fremen-fi/tnt/go/audio; this is a thin
// wrapper that adds app-level logging.
func (n *AudioNormalizer) calculateDynamicsScore(inputPath string) *audio.DynamicsScoreAnalysis {
	n.appLog.Write(fmt.Sprintf("→ Calculating Dynamics Score: %s", filepath.Base(inputPath)))

	result, err := audio.CalculateDynamicsScore(ffmpeg.Path, inputPath)
	if err != nil {
		n.logFile.Write(fmt.Sprintf("DS calculation failed: %v", err))
		return nil
	}

	n.logFile.Write(fmt.Sprintf("DS Analysis - RMS Peak: %.2f dB, RMS Level: %.2f dB, Crest: %.2f",
		result.RMSPeak, result.RMSLevel, result.CrestFactor))
	n.logFile.Write(fmt.Sprintf("Dynamics Score: %.2f", result.DynamicsScore))

	return result
}
