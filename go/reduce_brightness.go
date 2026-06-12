package main

import (
	"fmt"

	"github.com/fremen-fi/tnt/go/audio"
)

// brightnessTiers maps each lossy codec to the bitrate thresholds (kbps) at
// which brightness reduction and the encoder pre-limiter step up in strength.
// Codecs absent from the map (PCM, flac, native aac) never engage either stage.
type bitrateTiers struct {
	mild, mid, hard int
}

var brightnessTiers = map[string]bitrateTiers{
	"libmp3lame": {mild: 192, mid: 160, hard: 128},
	"aac_at":     {mild: 128, mid: 96, hard: 64},
	"libfdk_aac": {mild: 128, mid: 96, hard: 64},
	"libopus":    {mild: 96, mid: 64, hard: 48},
}

// brightnessStrength returns the brightness-reduction / encoder pre-limiter
// strength (0–3) for a codec at the given bitrate. The bitrate MUST be in
// kbps: processFile carries full bps for the needsFullNumber codecs, and
// comparing bps against these kbps tiers silently disabled the whole stage.
func brightnessStrength(codec string, bitrateKbps int) int {
	t, ok := brightnessTiers[codec]
	if !ok {
		return 0
	}
	switch {
	case bitrateKbps <= t.hard:
		return 3
	case bitrateKbps <= t.mid:
		return 2
	case bitrateKbps <= t.mild:
		return 1
	}
	return 0
}

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
