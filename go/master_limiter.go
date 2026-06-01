package main

import (
	"fmt"
	"github.com/fremen-fi/tnt/go/audio"
)

func (n *AudioNormalizer) buildPreDynSoftLimiter(strength int) string {
	filter := audio.BuildSoftLimiterPreDyn(strength)
	if filter != "" {
		n.logToFile(n.logFile, fmt.Sprintf("Pre-limit: %s", filter))
	}
	return filter
}
