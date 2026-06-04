package audio

import (
	"fmt"
	"strings"
)

// Reduce brightness before lossy codecs. Should not be used as an intermediary stage.
// Values are calibrated for Apple AAC.
func ReduceBrightnessLossy(strength int) (filter string, passBelowFreq, passAboveFreq, poles int) {

	passBelowFreq = 17500
	passAboveFreq = 10
	poles = 2

	reduceInDb := -2       // amount of db to shave off
	reduceFromFreq := 7500 // from where to start shaving

	switch strength {
	case 0:
		passBelowFreq = 20250
		passAboveFreq = 10
		poles = 2
		reduceInDb = 0
	case 1:
		passBelowFreq = 17500
		passAboveFreq = 15
		reduceInDb = -2
	case 2:
		passBelowFreq = 16000
		passAboveFreq = 25
		reduceInDb = -2
		reduceFromFreq = 6000
	case 3:
		passBelowFreq = 14500
		passAboveFreq = 36
		poles = 1
		reduceInDb = -3
		reduceFromFreq = 5000
	}

	// apply a high-shelf reduction
	shelf := fmt.Sprintf("highshelf=g=%d:f=%d:precision=f64", reduceInDb, reduceFromFreq)

	// apply a low-pass
	lowpass := fmt.Sprintf("lowpass=f=%d:p=%d:precision=f64", passBelowFreq, poles)
	highpass := fmt.Sprintf("highpass=f=%d:p=%d:precision=f64", passAboveFreq, poles)

	filter = strings.Join([]string{shelf, lowpass, highpass}, ",")

	return filter, passBelowFreq, passAboveFreq, poles
}

// As opposed to the function above, this function is meant for an intermediary stage to mitigate distortion-like effects when using a high-ratio compressor and limiter.
func ReduceBrightnessPCM(strength int) (filter string) {
	hsf := 20500
	lsf := 10
	hfg := 0.
	lfg := 0.

	switch strength {
	case 1:
		hsf = 8500
		lsf = 10
		hfg = -1.5
	case 2:
		hsf = 7500
		lsf = 10
		hfg = -2.0
	case 3:
		hsf = 6000
		lsf = 10
		hfg = -2.5
	}

	filter = fmt.Sprintf("highshelf=f=%d:g=%.2f,lowshelf=f=%d:g=%.2f", hsf, hfg, lsf, lfg)

	return filter
}
