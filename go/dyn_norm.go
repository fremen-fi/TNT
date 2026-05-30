package main

import (
	"fmt"
	"math"

	"github.com/fremen-fi/tnt/go/audio"
)

// DynaudnormParams is an alias for the public audio package type. The dynaudnorm
// logic lives in github.com/fremen-fi/tnt/go/audio; these receiver methods are
// thin wrappers that add app-level logging.
type DynaudnormParams = audio.DynaudnormParams

// analyzeDynaudnormParams creates dynaudnorm parameters from existing dynamics
// analysis.
func (n *AudioNormalizer) analyzeDynaudnormParams(analysis *DynamicsAnalysis) *DynaudnormParams {
	params := audio.AnalyzeDynaudnormParams(analysis)
	if params == nil {
		return nil
	}

	n.logToFile(n.logFile, fmt.Sprintf("Dynaudnorm params - RMS Peak: %.2f dB, Noise Floor: %.2f dB",
		params.RMSPeakDB, params.NoiseFloorDB))
	n.logToFile(n.logFile, fmt.Sprintf("Calculated - Target RMS: %.6f (%.2f dB), Threshold: %.6f (%.2f dB)",
		params.TargetRMS, 20*math.Log10(params.TargetRMS),
		params.Threshold, 20*math.Log10(params.Threshold)))

	return params
}

// buildDynaudnormFilter creates the dynaudnorm filter string.
func (n *AudioNormalizer) buildDynaudnormFilter(params *DynaudnormParams) string {
	filter := audio.BuildDynaudnormFilter(params)
	if filter != "" {
		n.logToFile(n.logFile, fmt.Sprintf("Dynaudnorm filter: %s", filter))
	}
	return filter
}
