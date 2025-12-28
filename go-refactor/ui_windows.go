//go:build windows

package main

func getPlatformFormats() []string {
	return []string{"Opus", "AAC", "MPEG-II L3", "PCM", "FLAC"}
}

func getPlatformCodecMap() map[string]string {
	return map[string]string{
		"AAC": "libfdk_aac",
	}
}