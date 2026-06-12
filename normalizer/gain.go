package normalizer

// Streaming gain application and sample-peak limiter.
//
// The limiter here is a sample-peak (not true-peak) limiter. For the podcore
// transcode pipeline the encode pass applies FFmpeg loudnorm+alimiter which
// enforces the TP ceiling; this stage only prevents gross over-shoots before
// encoding and adds negligible CPU cost compared to the FIR-based TP approach.

import (
	"io"
	"math"
)

// ApplyGainAndLimit reads interleaved f32 stereo samples from src, multiplies
// each sample by gainLinear, then hard-clips at |peakCeiling| (linear, e.g.
// 0.5623 for −5 dBFS), and writes the result to dst.
//
// Returns the number of sample frames processed and the highest |sample| seen
// BEFORE the limiter (to let the caller detect how much headroom was consumed).
func ApplyGainAndLimit(src io.Reader, dst io.Writer, gainLinear, peakCeiling float64) (frames int64, preGainPeakDB float64, err error) {
	buf := make([]float32, 8192) // 4096 stereo frames per chunk
	var maxAbs float64

	for {
		n, rerr := readF32LE(src, buf)
		if n > 0 {
			for i := range n {
				s := float64(buf[i]) * gainLinear
				if a := math.Abs(s); a > maxAbs {
					maxAbs = a
				}
				if s > peakCeiling {
					s = peakCeiling
				} else if s < -peakCeiling {
					s = -peakCeiling
				}
				buf[i] = float32(s)
			}
			if werr := writeF32LE(dst, buf[:n]); werr != nil {
				return frames, 0, werr
			}
			frames += int64(n / 2) // stereo: 2 samples per frame
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			return frames, 0, rerr
		}
	}

	if maxAbs > 0 {
		preGainPeakDB = 20 * math.Log10(maxAbs)
	} else {
		preGainPeakDB = -math.MaxFloat64
	}
	return frames, preGainPeakDB, nil
}
