//go:build windows

package main

import( 
	_ "embed"
	)

//go:embed ffmpegSource/ffmpeg.exe
var ffmpegBinary []byte

var codecMap = map[string]string{
	"Opus": "libopus",
	"AAC": "libfdk_aac",
	"MPEG-II L3": "libmp3lame",
	"PCM": "PCM",
	"FLAC": "flac",
	"Small file (AAC 256kbps)": "libfdk_aac",
	"Most compatible (MP3 160kbps)": "libmp3lame",
	"Production (PCM 48kHz/24bit)": "PCM",
}
