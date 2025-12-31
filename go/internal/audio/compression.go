package audio

import "math"

// GetCompressionModifiers returns compression parameter multipliers based on Dynamics Score
func GetCompressionModifiers(ds float64) CompressionModifiers {
	mods := CompressionModifiers{
		AttackMultiplier:  1.0,
		ReleaseMultiplier: 1.0,
		RatioMultiplier:   1.0,
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
		excess := math.Min(ds-21.0, 79.0)
		scaleFactor := 2.0 + (2.0 * excess / 79.0)

		mods.AttackMultiplier = 1.0 / scaleFactor
		mods.ReleaseMultiplier = 1.0 / scaleFactor
		mods.RatioMultiplier = 4.0 + (4.0 * excess / 79.0)
	}

	return mods
}

// GetBaseRatioFromCrest returns base compression ratio based on crest factor
func GetBaseRatioFromCrest(crest float64) float64 {
	if crest <= 3.0 {
		return 1.4
	} else if crest <= 5.0 {
		return 2.0
	} else if crest <= 8.0 {
		return 4.0
	} else {
		if crest >= 16.0 {
			return 8.0
		}
		return 4.0 + (4.0 * (crest - 8.0) / 8.0)
	}
}

// CalculateMakeupGain computes makeup gain based on expected gain reduction
func CalculateMakeupGain(analysis *DynamicsAnalysis, thresholdDb float64, ratio float64) float64 {
	if analysis == nil || ratio <= 1.0 {
		return 1.0
	}

	// Estimate average gain reduction
	avgLevel := analysis.RMSLevel
	if avgLevel > thresholdDb {
		// Signal above threshold - calculate reduction
		excessDb := avgLevel - thresholdDb
		reductionDb := excessDb - (excessDb / ratio)
		// Apply partial makeup (80% of reduction)
		makeupDb := reductionDb * 0.8
		return math.Pow(10, makeupDb/20)
	}

	return 1.0
}

// ClampCompressorParams ensures all compressor parameters are within valid ranges
func ClampCompressorParams(thresholdLin, ratio, attackMs, releaseMs, makeupLin float64) (float64, float64, float64, float64, float64) {
	// Threshold: 0.00097563 (-60dB) to 1.0 (0dB)
	if thresholdLin < 0.00097563 {
		thresholdLin = 0.00097563
	}
	if thresholdLin > 1.0 {
		thresholdLin = 1.0
	}

	// Ratio: 1.0 to 20.0
	if ratio < 1.0 {
		ratio = 1.0
	}
	if ratio > 20.0 {
		ratio = 20.0
	}

	// Attack: 0.01ms to 2000ms
	if attackMs < 0.01 {
		attackMs = 0.01
	}
	if attackMs > 2000.0 {
		attackMs = 2000.0
	}

	// Release: 0.01ms to 9000ms
	if releaseMs < 0.01 {
		releaseMs = 0.01
	}
	if releaseMs > 9000.0 {
		releaseMs = 9000.0
	}

	// Makeup: 1.0 to 64.0
	if makeupLin < 1.0 {
		makeupLin = 1.0
	}
	if makeupLin > 64.0 {
		makeupLin = 64.0
	}

	return thresholdLin, ratio, attackMs, releaseMs, makeupLin
}

// GetKneeFromRatio returns appropriate knee value based on compression ratio
func GetKneeFromRatio(ratio float64) float64 {
	if ratio < 1.0 {
		return 1.0
	} else if ratio < 2.0 {
		return 2.0
	} else if ratio < 4.0 {
		return 3.0
	} else if ratio < 8.0 {
		return 4.0
	} else if ratio < 12.0 {
		return 6.0
	} else if ratio >= 12.0 {
		return 7.5
	}
	return 4.0
}

// DbToLinear converts decibels to linear amplitude
func DbToLinear(db float64) float64 {
	return math.Pow(10, db/20)
}

// LinearToDb converts linear amplitude to decibels
func LinearToDb(linear float64) float64 {
	if linear <= 0 {
		return -100.0
	}
	return 20 * math.Log10(linear)
}
