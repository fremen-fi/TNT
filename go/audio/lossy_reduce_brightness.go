package audio

import (
	"fmt"
	"strings"
)

// reduce brightness before lossy codecs or heavy compression

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
