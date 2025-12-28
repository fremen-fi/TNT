//go:build darwin && amd64

package platform

import _ "embed"

//go:embed ffmpegSource/ffmpegMacX86
var FFmpegBinary []byte
