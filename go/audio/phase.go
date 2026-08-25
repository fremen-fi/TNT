package audio

import (
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
)

// PhaseCheck runs an astats pass over the first 30 seconds of inputPath and
// reports whether the two channels appear phase-inverted, along with the
// computed phase offset. ffmpegPath is the path to the ffmpeg binary.
func PhaseCheck(ffmpegPath, inputPath string) (inverted bool, offset float64, err error) {
	output, err := runPhaseCheck(ffmpegPath, inputPath)
	if err != nil {
		return false, 0, err
	}

	ch1Min, ch1Max, ch2Min, ch2Max, err := parsePhaseCheck(output)
	if err != nil {
		return false, 0, err
	}

	offset = calculatePhaseOffset(ch1Min, ch1Max, ch2Min, ch2Max)
	inverted = offset < 0.01

	return inverted, offset, nil
}

func runPhaseCheck(ffmpegPath, inputPath string) (string, error) {
	cmd := exec.Command(ffmpegPath, "-i", inputPath, "-vn", "-t", "30", "-af", "astats", "-f", "null", "-")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ffmpeg astats failed: %w", err)
	}
	return string(output), nil
}

func parsePhaseCheck(output string) (ch1Min, ch1Max, ch2Min, ch2Max float64, err error) {
	// Channel 1
	ch1Re := regexp.MustCompile(`(?s)Channel: 1.*?Min level:\s+([-\d.]+).*?Max level:\s+([-\d.]+)`)
	if m := ch1Re.FindStringSubmatch(output); len(m) > 2 {
		ch1Min, _ = strconv.ParseFloat(m[1], 64)
		ch1Max, _ = strconv.ParseFloat(m[2], 64)
	} else {
		return 0, 0, 0, 0, fmt.Errorf("channel 1 not found")
	}

	// Channel 2
	ch2Re := regexp.MustCompile(`(?s)Channel: 2.*?Min level:\s+([-\d.]+).*?Max level:\s+([-\d.]+)`)
	if m := ch2Re.FindStringSubmatch(output); len(m) > 2 {
		ch2Min, _ = strconv.ParseFloat(m[1], 64)
		ch2Max, _ = strconv.ParseFloat(m[2], 64)
	} else {
		return 0, 0, 0, 0, fmt.Errorf("channel 2 not found")
	}

	return ch1Min, ch1Max, ch2Min, ch2Max, nil
}

func calculatePhaseOffset(ch1Min, ch1Max, ch2Min, ch2Max float64) float64 {
	diff1 := math.Abs(math.Abs(ch1Min) - math.Abs(ch2Max))
	diff2 := math.Abs(math.Abs(ch1Max) - math.Abs(ch2Min))
	return math.Max(diff1, diff2)
}
