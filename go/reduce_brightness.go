package main

import (
	"fmt"

	"github.com/fremen-fi/tnt/go/audio"
)

func (n *AudioNormalizer) buildBrightnessReduceFilterForLossy(strength int) string {
	n.logToFile(n.logFile, fmt.Sprintf("Reducing brightness because of a lossy codec."))
	n.logToFile(n.logFile, fmt.Sprintf("Strength set to %d", strength))

	filter, pbf, paf, poles := audio.ReduceBrightnessLossy(strength)
	n.logToFile(n.logFile, fmt.Sprintf("pass-below frequency: %d, and pass-above f: %d", pbf, paf))
	n.logToFile(n.logFile, fmt.Sprintf("poles: %d", poles))

	return filter
}
