package audio

import (
	"fmt"
	"math"
)

// CalculateDynaudnormParams creates dynaudnorm parameters from dynamics analysis
func CalculateDynaudnormParams(analysis *DynamicsAnalysis) *DynaudnormParams {
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

	// Calculate threshold: Noise_floor + 12dB, converted to linear
	thresholdDB := params.NoiseFloorDB + 12.0
	params.Threshold = math.Pow(10, thresholdDB/20)

	// Clamp to valid ranges (0.0-1.0)
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

	return params
}

// BuildDynaudnormFilter creates the dynaudnorm filter string from params
func BuildDynaudnormFilter(params *DynaudnormParams) string {
	if params == nil {
		return ""
	}

	return fmt.Sprintf(
		"dynaudnorm=framelen=650:gausssize=36:targetrms=%.6f:threshold=%.6f:altboundary=true:overlap=0.95",
		params.TargetRMS,
		params.Threshold,
	)
}
