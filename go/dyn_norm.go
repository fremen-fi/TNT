package main

import (
	"fmt"
	"math"
)

// DynaudnormParams holds the calculated parameters for dynaudnorm
type DynaudnormParams struct {
	TargetRMS  float64 // Linear scale (0.0-1.0)
	Threshold  float64 // Linear scale (0.0-1.0)
	RMSPeakDB  float64 // dB value for reference
	NoiseFloorDB float64 // dB value for reference
}

// analyzeDynaudnormParams measures the audio to calculate optimal dynaudnorm settings
// analyzeDynaudnormParams creates dynaudnorm parameters from existing dynamics analysis
func (n *AudioNormalizer) analyzeDynaudnormParams(analysis *DynamicsAnalysis) *DynaudnormParams {
	if analysis == nil {
		return nil
	}
	
	params := &DynaudnormParams{
		RMSPeakDB:    analysis.RMSPeak,
		NoiseFloorDB: analysis.NoiseFloor,
	}
	
	// Calculate target RMS: RMS_peak - 6dB, converted to linear
	targetDB := params.RMSPeakDB - 6.0
	params.TargetRMS = math.Pow(10, targetDB/20)
	
	// Calculate threshold: Noise_floor + 6dB, converted to linear
	thresholdDB := params.NoiseFloorDB + 12.0
	params.Threshold = math.Pow(10, thresholdDB/20)
	
	// Clamp to valid ranges
	if params.TargetRMS < 0.0 {
		params.TargetRMS = 0.0
	}
	if params.TargetRMS > 1.0 {
		params.TargetRMS = 1.0
	}
	if params.Threshold < 0.0 {
		params.Threshold = 0.0
	}
	if params.Threshold > 1.0 {
		params.Threshold = 1.0
	}
	
	n.logToFile(n.logFile, fmt.Sprintf("Dynaudnorm params - RMS Peak: %.2f dB, Noise Floor: %.2f dB", 
		params.RMSPeakDB, params.NoiseFloorDB))
	n.logToFile(n.logFile, fmt.Sprintf("Calculated - Target RMS: %.6f (%.2f dB), Threshold: %.6f (%.2f dB)",
		params.TargetRMS, 20*math.Log10(params.TargetRMS),
		params.Threshold, 20*math.Log10(params.Threshold)))
	
	return params
}

// buildDynaudnormFilter creates the dynaudnorm filter string
func (n *AudioNormalizer) buildDynaudnormFilter(params *DynaudnormParams) string {
	if params == nil {
		return ""
	}
	
	// Build dynaudnorm with calculated parameters
	// framelen: default 500ms (can experiment later)
	// gausssize: default 31 (can experiment later)
	// targetrms: calculated from RMS_peak - 6dB
	// threshold: calculated from Noise_floor + 6dB
	// altboundary: enabled for smooth fade in/out altboundary=1:
	// overlap: 0.75 for smooth gain transitions
	
	filter := fmt.Sprintf(
		"dynaudnorm=framelen=800:gausssize=41:targetrms=%.6f:threshold=%.6f:altboundary=true:overlap=0.95",
		params.TargetRMS,
		params.Threshold,
	)
	
	n.logToFile(n.logFile, fmt.Sprintf("Dynaudnorm filter: %s", filter))
	
	return filter
}