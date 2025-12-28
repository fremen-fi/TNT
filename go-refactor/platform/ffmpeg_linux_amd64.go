//go:build linux && amd64

package platform

import _ "embed"

//go:embed ffmpegSource/ffmpegLinuxAmd64
var FFmpegBinary []byte
