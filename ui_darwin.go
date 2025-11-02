//go:build darwin

package main

func getPlatformFormats() []string {
	return []string{"Opus", "AAC (Fraunhofer)", "AAC (Apple)", "MPEG-II L3", "PCM"}
}

func getPlatformCodecMap() map[string]string {
	return map[string]string{
		"AAC (Fraunhofer)": "libfdk_aac",
		"AAC (Apple)": "aac_at",
	}
}