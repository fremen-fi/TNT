package audio

import (
	"encoding/binary"
	"math"
)

// SamplesFromFloat32 converts raw PCM bytes (32-bit float, interleaved stereo)
// into per-channel sample slices.
//
// Layout assumption: [L0, R0, L1, R1, ...]
// Each sample is 4 bytes, little-endian IEEE 754.
func SamplesFromFloat32(raw []byte) (left, right []float64) {
	// 2 channels * 4 bytes = 8 bytes per frame
	nFrames := len(raw) / 8
	left = make([]float64, nFrames)
	right = make([]float64, nFrames)

	for i := range nFrames {
		offset := i * 8
		lBits := binary.LittleEndian.Uint32(raw[offset : offset+4])
		rBits := binary.LittleEndian.Uint32(raw[offset+4 : offset+8])
		left[i] = float64(math.Float32frombits(lBits))
		right[i] = float64(math.Float32frombits(rBits))
	}
	return
}

// SamplesFromInt24 converts raw PCM bytes (24-bit signed int, interleaved stereo)
// into per-channel sample slices normalised to [-1.0, 1.0].
//
// Layout assumption: [L0, R0, L1, R1, ...]
// Each sample is 3 bytes, little-endian.
func SamplesFromInt24(raw []byte) (left, right []float64) {
	nFrames := len(raw) / 6 // 2 channels * 3 bytes
	left = make([]float64, nFrames)
	right = make([]float64, nFrames)

	// Normalize
	const scale = 1.0 / 8388608.0 // 2^23
	for i := range nFrames {
		offset := i * 6

		// Read 3 bytes and sign-extend to int32.
		lRaw := uint32(raw[offset]) | uint32(raw[offset+1])<<8 | uint32(raw[offset+2])<<16
		rRaw := uint32(raw[offset+3]) | uint32(raw[offset+4])<<8 | uint32(raw[offset+5])<<16

		// Sign-extend: if bit 23 is set, fill upper 8 bits with 1s.
		lSample := int32(lRaw)
		if lRaw&0x800000 != 0 {
			lSample = int32(lRaw | 0xFF000000)
		}
		rSample := int32(rRaw)
		if rRaw&0x800000 != 0 {
			rSample = int32(rRaw | 0xFF000000)
		}

		left[i] = float64(lSample) * scale
		right[i] = float64(rSample) * scale
	}
	return
}

// SamplesFromInt32 converts raw PCM bytes (32-bit signed int, interleaved
// stereo) into per-channel sample slices normalised to [-1.0, 1.0].
//
// Layout assumption: [L0, R0, L1, R1, ...]
// Each sample is 4 bytes, little-endian.
func SamplesFromInt32(raw []byte) (left, right []float64) {
	nFrames := len(raw) / 8 // 2 channels * 4 bytes
	left = make([]float64, nFrames)
	right = make([]float64, nFrames)

	const scale = 1.0 / 2147483648.0 // 2^31
	for i := range nFrames {
		offset := i * 8
		l := int32(binary.LittleEndian.Uint32(raw[offset : offset+4]))
		r := int32(binary.LittleEndian.Uint32(raw[offset+4 : offset+8]))
		left[i] = float64(l) * scale
		right[i] = float64(r) * scale
	}
	return
}

// SamplesFromInt16 converts raw PCM bytes (16-bit signed int, interleaved
// stereo) into per-channel sample slices normalised to [-1.0, 1.0].
//
// Layout assumption: [L0, R0, L1, R1, ...]
// Each sample is 2 bytes, little-endian.
func SamplesFromInt16(raw []byte) (left, right []float64) {
	nFrames := len(raw) / 4 // 2 channels * 2 bytes
	left = make([]float64, nFrames)
	right = make([]float64, nFrames)

	const scale = 1.0 / 32768.0 // 2^15
	for i := range nFrames {
		offset := i * 4
		l := int16(binary.LittleEndian.Uint16(raw[offset : offset+2]))
		r := int16(binary.LittleEndian.Uint16(raw[offset+2 : offset+4]))
		left[i] = float64(l) * scale
		right[i] = float64(r) * scale
	}
	return
}

// Correlation computes the Pearson correlation coefficient between left and
// right channels over the provided sample window.
//
// Returns a value in [-1.0, 1.0]:
//
//	+1.0 = perfectly correlated (mono)
//	 0.0 = uncorrelated
//	-1.0 = perfectly anti-phase
//
// Returns 0 if either channel has zero energy (silence).
func Correlation(left, right []float64) float64 {
	if len(left) != len(right) || len(left) == 0 {
		return 0
	}

	var sumLR, sumL2, sumR2 float64
	for i := range left {
		sumLR += left[i] * right[i]
		sumL2 += left[i] * left[i]
		sumR2 += right[i] * right[i]
	}

	denom := math.Sqrt(sumL2 * sumR2)
	if denom == 0 {
		return 0
	}
	return sumLR / denom
}

/* TODO, USE THIS TEST
func main() {
    // Minimal smoke test: synthesise one frame of in-phase sine at 1 kHz,
    // 48 kHz sample rate, as float32 PCM.
    const (
        sampleRate = 48000
        freq       = 1000.0
        nFrames    = 480 // 10 ms
    )

    raw := make([]byte, nFrames*8)
    for i := range nFrames {
        v := float32(math.Sin(2 * math.Pi * freq * float64(i) / sampleRate))
        bits := math.Float32bits(v)
        offset := i * 8
        binary.LittleEndian.PutUint32(raw[offset:], bits)   // L
        binary.LittleEndian.PutUint32(raw[offset+4:], bits) // R (identical → ρ=1)
    }

    left, right := SamplesFromFloat32(raw)
    rho := Correlation(left, right)
    fmt.Printf("float32 correlation (in-phase):   %.6f\n", rho) // expect ~1.000000

    // Anti-phase: negate R.
    for i := range nFrames {
        offset := i * 8
        bits := binary.LittleEndian.Uint32(raw[offset:])
        v := -math.Float32frombits(bits)
        binary.LittleEndian.PutUint32(raw[offset+4:], math.Float32bits(float32(v)))
    }
    left, right = SamplesFromFloat32(raw)
    rho = Correlation(left, right)
    fmt.Printf("float32 correlation (anti-phase): %.6f\n", rho) // expect ~-1.000000
}
*/
