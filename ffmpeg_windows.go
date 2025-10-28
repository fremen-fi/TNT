//go:build windows

package main

import( 
	_ "embed"
	)

//go:embed ffmpegSource/ffmpeg.exe
var ffmpegBinary []byte

var codecMap = map[string]string{
	"Opus": "libopus",
	"AAC": "aac",
	"MPEG-II L3": "libmp3lame",
	"PCM": "PCM",
	"Small file (AAC 256kbps)": "aac",
	"Most compatible (MP3 160kbps)": "libmp3lame",
	"Production (PCM 48kHz/24bit)": "PCM",
}

