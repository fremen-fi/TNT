package main

import (
	"context"
	"runtime"
	"sync"

	"github.com/fremen-fi/tnt/go/internal/telemetry"
)

type AudioNormalizer struct {
	ctx context.Context

	files     []string
	outputDir string
	inputDir  string
	batchMode bool
	mutex     sync.Mutex

	advancedMode          bool
	normalizationStandard Standard
	format                string
	sampleRate            string
	bitDepth              string
	bitrate               string
	useLoudnorm           bool
	customLoudnorm        bool
	normalizeTarget       string
	normalizeTargetTp     string
	isSpeech              bool
	writeTags             bool
	noTranscode           bool
	dataCompLevel         int8
	dynamicsPreset        string
	eqPreset              string
	bypassProc            bool
	dynNorm               bool
	phaseCheck            bool
	simplePreset          string
	multibandFilter       string
	videoAction           string

	watching     bool
	watcherStop  chan bool
	jobQueue     chan string
	watcherMutex sync.Mutex

	logFile *LogIntoFile
	appLog  *LogApp

	telemetry        *telemetry.Client
	telemetryEnabled bool
}

func (n *AudioNormalizer) GetPlatformFormats() []string {
	if runtime.GOOS == "darwin" {
		return []string{"Opus", "AAC (Fraunhofer)", "AAC (Apple)", "MPEG-II L3", "PCM", "FLAC"}
	}
	return []string{"Opus", "AAC", "MPEG-II L3", "PCM", "FLAC"}
}

var platformCodecMap = map[string]string{
	"AAC (Fraunhofer)": "libfdk_aac",
	"AAC (Apple)":      "aac_at",
	"AAC":              "libfdk_aac",
}
