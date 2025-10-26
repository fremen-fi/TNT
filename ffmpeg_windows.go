//go:build windows

package main

import _ "embed"

//go:embed ffmpeg.exe
var ffmpegBinary []byte