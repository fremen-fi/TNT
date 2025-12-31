//go:build linux && arm64

package platform

import _ "embed"

//go:embed ffmpegSource/ffmpegLinuxArm64
var FFmpegBinary []byte
