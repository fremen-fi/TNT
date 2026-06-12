package audio

import (
	"fmt"
	"os/exec"
)

// CalculateDynamicsScore runs ffmpeg's astats filter over inputPath and returns
// the parsed Dynamics Score analysis. ffmpegPath is the path to the ffmpeg
// binary.
func CalculateDynamicsScore(ffmpegPath, inputPath string) (*DynamicsScoreAnalysis, error) {
	cmd := exec.Command(
		ffmpegPath,
		"-i", inputPath,
		"-af", "astats",
		"-f", "null",
		"-",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg astats failed: %w", err)
	}

	return ParseDynamicsScore(string(output)), nil
}
