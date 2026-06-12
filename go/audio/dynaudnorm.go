package audio

import (
	"fmt"
	"math"
)

// CalculateDynaudnormParams creates dynaudnorm parameters from dynamics
// analysis. Returns nil if analysis is nil.
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
	params.TargetRMS = max(min(params.TargetRMS, 1.0), 0.0)
	params.Threshold = max(min(params.Threshold, 1.0), 0.0)

	return params
}

// AnalyzeDynaudnormParams derives dynaudnorm parameters from an existing
// dynamics analysis. It is the canonical name used by the TNT app and is
// equivalent to CalculateDynaudnormParams.
func AnalyzeDynaudnormParams(analysis *DynamicsAnalysis) *DynaudnormParams {
	return CalculateDynaudnormParams(analysis)
}

// BuildDynaudnormFilter creates the dynaudnorm filter string from params.
// Returns an empty string if params is nil.
func BuildDynaudnormFilter(params *DynaudnormParams, isSpeech bool) string {
	if params == nil || isSpeech {
		return ""
	}

	// gausssize must be odd — ffmpeg coerced the old 36 to 37 with a warning,
	// so 37 is spelled out to keep the effective behaviour and lose the warning.
	return fmt.Sprintf(
		"dynaudnorm=framelen=650:gausssize=37:targetrms=%.6f:threshold=%.6f:altboundary=true:overlap=0.95",
		params.TargetRMS,
		params.Threshold,
	)
}
