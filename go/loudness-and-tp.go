package main

import (
	"fmt"
	"github.com/fremen-fi/tnt/go/audio"
)

func (n *AudioNormalizer) LUFS(path string) (float64, float64, float64, error) {
	l, err := audio.MeasureLUFS(path)
	if err != nil {
		n.appLog.Write("There was an error in the loudness measurement.")
		n.logFile.Write(fmt.Sprintf("There was an error in the loudness measurement: %v", err))
		return 0, 0, 0, err
	}
	n.logFile.Write(fmt.Sprintf("LUFS-I: %.1f\nTP: %.1f\nLRA: %.1f\n", l.Integrated, l.TruePeak, l.LRA))
	return l.Integrated, l.TruePeak, l.LRA, nil
}

func (n *AudioNormalizer) Gain(path string, offset float64) error {
	if err := audio.Gain(path, offset); err != nil {
		n.appLog.Write("There was an error applying gain.")
		n.logFile.Write(fmt.Sprintf("There was an error applying gain: %v", err))
		return err
	}
	n.logFile.Write(fmt.Sprintf("Applied %.2f dB gain to %s", offset, path))
	return nil
}
