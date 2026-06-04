package audio

import (
	"fmt"
	"math"
)

func BuildSoftLimiterPreDyn(strength int) string {
	softLimiterThreshold := -3.2 // threshold in dB
	softLimiterAttack := 20      // in ms
	softLimiterRel := 80         // in ms

	// values calibrated for Apple AAC using bitrates
	// 400 (no processing),
	// 128 (case 1),
	// 96 (case 2) and
	// 64 kilobits per second (case 3).
	// They all have a True Peak of -1.1...-1 dB TP.
	// Other encoders and bitrates might result in different results.
	switch strength {
	case 1:
		softLimiterThreshold = -3.2
		softLimiterAttack = 18
		softLimiterRel = 75
	case 2:
		softLimiterThreshold = -4.2
		softLimiterAttack = 10
		softLimiterRel = 60
	case 3:
		softLimiterThreshold = -10.0
		softLimiterAttack = 8
		softLimiterRel = 16
	}

	thresholdInLin := math.Pow(10, float64(softLimiterThreshold)/20.0)
	return fmt.Sprintf("alimiter=limit=%.6f:attack=%d:release=%d:asc=true:asc_level=0.5:level=false", thresholdInLin, softLimiterAttack, softLimiterRel)
}
