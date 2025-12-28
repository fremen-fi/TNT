package config

// CodecMap maps UI codec names to FFmpeg encoder names
// This is shared across all platforms
var CodecMap = map[string]string{
	"Opus":                          "libopus",
	"AAC":                           "libfdk_aac",
	"MPEG-II L3":                    "libmp3lame",
	"PCM":                           "PCM",
	"FLAC":                          "flac",
	"Small file (AAC 256kbps)":      "libfdk_aac",
	"Most compatible (MP3 160kbps)": "libmp3lame",
	"Production (PCM 48kHz/24bit)":  "PCM",
}

// GetCodec returns the FFmpeg encoder name for a given UI codec name
func GetCodec(uiName string) string {
	if codec, ok := CodecMap[uiName]; ok {
		return codec
	}
	return uiName
}
