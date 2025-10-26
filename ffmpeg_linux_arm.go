//go:build linux && arm64

package main

import _ "embed"

//go:embed ffmpegLinuxArm64
var ffmpegBinary []byte