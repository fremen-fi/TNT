package main

import (
	"fmt"

	"github.com/fremen-fi/tnt/go/audio"
)

func (n *AudioNormalizer) buildBrightnessReduceFilterForLossy(strength int) string {
	n.logFile.Write("Reducing brightness because of a lossy codec.")
	n.logFile.Write(fmt.Sprintf("Strength set to %d", strength))

	filter, pbf, paf, poles := audio.ReduceBrightnessLossy(strength)
	n.logFile.Write(fmt.Sprintf("pass-below frequency: %d, and pass-above f: %d", pbf, paf))
	n.logFile.Write(fmt.Sprintf("poles: %d", poles))

	return filter
}

// NOT CALLED, TODO
func (n *AudioNormalizer) buildPCMBrightnessReductionFilter(strength int) string {
	n.logFile.Write("Reducing brightness because of a dynamics processor.")
	n.logFile.Write(fmt.Sprintf("Strength set to %d", strength))

	filter := audio.ReduceBrightnessPCM(strength)
	n.logFile.Write(fmt.Sprintf("reduced brightness before a dynamics processor: %s", filter))

	return filter
}
