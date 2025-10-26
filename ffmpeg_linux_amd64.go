//go:build linux && amd64

package main

import _ "embed"

//go:embed ffmpegLinuxAmd64
var ffmpegBinary []byte