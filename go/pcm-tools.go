package main

import (
	"fmt"

	"github.com/fremen-fi/tnt/go/audio"
)

// Get the coherence of a PCM file
func (n *AudioNormalizer) PCMFileCoherence(f string) (float64, error) {
	defer n.timer()()
	n.logFile.Write("reading phase coherence...")
	rho, err := audio.GetCoherence(f)
	if err != nil {
		n.logFile.Write(fmt.Sprintf("Couldn't read coherence: %v", err))
		return 0, fmt.Errorf("cannot get coherence: %v", err)
	}
	n.logFile.Write(fmt.Sprintf("Got a phase coherence of %.1f", rho))
	return rho, nil
}
