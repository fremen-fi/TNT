// Package audio provides TNT's reusable audio analysis and processing logic:
// Dynamic Score calculation, DS-driven compression parameter derivation,
// 10-band frequency analysis and EQ filter building, dynaudnorm parameter
// calculation, and a stereo phase check.
//
// Functions that invoke ffmpeg take the path to the ffmpeg binary as their
// first argument; nothing in this package hardcodes "ffmpeg" or depends on
// TNT-internal packages. The package is stdlib-only and builds with
// CGO_ENABLED=0.
package audio

// DynamicsAnalysis holds the results of astats analysis for a file.
type DynamicsAnalysis struct {
	PeakLevel   float64
	RMSPeak     float64
	RMSTrough   float64
	CrestFactor float64
	RMSLevel    float64
	NoiseFloor  float64
}

// FrequencyBandAnalysis holds per-band analysis for multiband compression.
type FrequencyBandAnalysis struct {
	BandName    string
	PeakLevel   float64
	RMSLevel    float64
	CrestFactor float64
}

// DynamicsScoreAnalysis holds the calculated Dynamics Score.
type DynamicsScoreAnalysis struct {
	RMSPeak       float64
	RMSLevel      float64
	CrestFactor   float64
	DynamicsScore float64
}

// CompressionModifiers holds multipliers applied based on Dynamics Score.
type CompressionModifiers struct {
	AttackMultiplier  float64
	ReleaseMultiplier float64
	RatioMultiplier   float64
}

// DynaudnormParams holds calculated parameters for the dynaudnorm filter.
type DynaudnormParams struct {
	TargetRMS    float64 // Linear scale (0.0-1.0)
	Threshold    float64 // Linear scale (0.0-1.0)
	RMSPeakDB    float64 // dB value for reference
	NoiseFloorDB float64 // dB value for reference
}

// FrequencyBand represents analyzed frequency response data for one band.
type FrequencyBand struct {
	Frequency   string  // e.g. "50Hz", "100Hz", "12.8kHz+"
	FilterType  string  // "lowpass", "bandpass", "highpass"
	RMSLevel    float64 // Average level in dB
	PeakLevel   float64 // Peak level in dB (for reference)
	CrestFactor float64 // Peak-to-RMS ratio
}
