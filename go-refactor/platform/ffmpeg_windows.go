//go:build windows

package platform

import _ "embed"

//go:embed ffmpegSource/ffmpeg.exe
var FFmpegBinary []byte
