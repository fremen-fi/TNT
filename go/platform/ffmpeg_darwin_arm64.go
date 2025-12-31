//go:build darwin && arm64

package platform

import _ "embed"

//go:embed ffmpegSource/ffmpegMacM
var FFmpegBinary []byte
