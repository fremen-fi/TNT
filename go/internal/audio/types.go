package audio

// DynamicsAnalysis holds the results of astats analysis for a file
type DynamicsAnalysis struct {
	PeakLevel    float64
	RMSPeak      float64
	RMSTrough    float64
	CrestFactor  float64
	DynamicRange float64
	RMSLevel     float64
	NoiseFloor   float64
}

// FrequencyBandAnalysis holds per-band analysis for multiband compression
type FrequencyBandAnalysis struct {
	BandName     string
	PeakLevel    float64
	RMSLevel     float64
	CrestFactor  float64
	DynamicRange float64
}

// DynamicsScoreAnalysis holds the calculated Dynamics Score
type DynamicsScoreAnalysis struct {
	RMSPeak       float64
	RMSLevel      float64
	CrestFactor   float64
	DynamicsScore float64
}

// CompressionModifiers holds multipliers applied based on Dynamics Score
type CompressionModifiers struct {
	AttackMultiplier  float64
	ReleaseMultiplier float64
	RatioMultiplier   float64
}

// DynaudnormParams holds calculated parameters for dynaudnorm filter
type DynaudnormParams struct {
	TargetRMS    float64 // Linear scale (0.0-1.0)
	Threshold    float64 // Linear scale (0.0-1.0)
	RMSPeakDB    float64 // dB value for reference
	NoiseFloorDB float64 // dB value for reference
}
