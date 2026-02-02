package audio

import (
	"github.com/fremen-fi/tnt/go/internal/ffmpeg"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"math"
)

func PhaseCheck(inputPath string, logFile *os.File) (inverted bool, offset float64, err error) {
	output, err := buildPhaseCheck(inputPath, logFile)
	if err != nil {
		return false, 0, err
	}

	ch1Min, ch1Max, ch2Min, ch2Max, err := parsePhaseCheck(output)
	if err != nil {
		return false, 0, err
	}

	offset = calculatePhaseOffset(ch1Min, ch1Max, ch2Min, ch2Max)
	inverted = offset < 0.01  // or whatever threshold you want

	return inverted, offset, nil
}

func buildPhaseCheck(inputPath string, logFile *os.File) (string, error) {
	cmd := ffmpeg.Command("-i", inputPath, "-af", "astats", "-f", "null", "-")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if logFile != nil {
			logFile.WriteString(fmt.Sprintf("astats failed: %v\n", err))
		}
		return "", err
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
