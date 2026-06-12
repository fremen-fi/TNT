package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
)

type wavHeader struct {
	audioFormat   uint16
	numChannels   uint16
	sampleRate    uint32
	bitsPerSample uint16
	dataSize      uint32
}

func readWAVHeader(r io.ReadSeeker) (wavHeader, error) {
	var riff [12]byte
	if _, err := io.ReadFull(r, riff[:]); err != nil {
		return wavHeader{}, err
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return wavHeader{}, fmt.Errorf("not a wave file")
	}

	var h wavHeader

	var fmtFound bool
	for {
		var chunkID [4]byte
		var chunkSize uint32
		if _, err := io.ReadFull(r, chunkID[:]); err != nil {
			return wavHeader{}, err
		}
		if err := binary.Read(r, binary.LittleEndian, &chunkSize); err != nil {
			return wavHeader{}, err
		}

		switch string(chunkID[:]) {
		case "fmt ":
			var fmtChunk struct {
				AudioFormat   uint16
				NumChannels   uint16
				SampleRate    uint32
				ByteRate      uint32
				BlockAlign    uint16
				BitsPerSample uint16
			}
			if err := binary.Read(r, binary.LittleEndian, &fmtChunk); err != nil {
				return wavHeader{}, err
			}

			// Effective format tag. For WAVE_FORMAT_EXTENSIBLE the real format
			// lives in the SubFormat GUID, not the AudioFormat field.
			format := fmtChunk.AudioFormat

			// Read (don't skip) any fmt-chunk extension. ffmpeg writes
			// pcm_s24le and pcm_s32le as WAVE_FORMAT_EXTENSIBLE (tag 0xFFFE),
			// so the SubFormat GUID is the only place the true PCM/float tag
			// appears. Skipping it — as the old code did — made every 24-bit
			// and 32-bit-int WAV fail the format check below.
			if chunkSize > 16 {
				ext := make([]byte, chunkSize-16)
				if _, err := io.ReadFull(r, ext); err != nil {
					return wavHeader{}, err
				}
				// ext layout for EXTENSIBLE:
				//   cbSize[2] validBitsPerSample[2] channelMask[4] subFormat[16]
				// The first two bytes of the SubFormat GUID are the real tag
				// (1 = PCM integer, 3 = IEEE float).
				if fmtChunk.AudioFormat == 0xFFFE && len(ext) >= 24 {
					format = binary.LittleEndian.Uint16(ext[8:10])
				}
			}

			if fmtChunk.NumChannels != 1 && fmtChunk.NumChannels != 2 {
				return wavHeader{}, fmt.Errorf("unsupported channel count: got %d, want mono or stereo", fmtChunk.NumChannels)
			}
			if format != 1 && format != 3 {
				return wavHeader{}, fmt.Errorf("audio format not supported: %d", format)
			}
			h.audioFormat = format
			h.numChannels = fmtChunk.NumChannels
			h.sampleRate = fmtChunk.SampleRate
			h.bitsPerSample = fmtChunk.BitsPerSample
			fmtFound = true

		case "data":
			if !fmtFound {
				return wavHeader{}, fmt.Errorf("data chunk before fmt chunk")
			}
			// ffmpeg's wav muxer CLAMPS the size fields to uint32 max when
			// the payload crosses the 4 GiB RIFF limit (and warns on
			// stderr, but still exits 0). Trusting the clamped header would
			// silently truncate hours of audio; refuse instead.
			if chunkSize == 0xFFFFFFFF {
				return wavHeader{}, fmt.Errorf("data chunk size is clamped at the 4 GiB RIFF limit — file is truncated or oversized")
			}
			h.dataSize = chunkSize
			return h, nil

		default:
			// RIFF chunks are word-aligned: an odd-sized chunk is followed by
			// a pad byte that chunkSize does not count. Skip it too, or the
			// next chunk header reads one byte off and we never find "data".
			skip := int64(chunkSize)
			if skip%2 == 1 {
				skip++
			}
			if _, err := r.Seek(skip, io.SeekCurrent); err != nil {
				return wavHeader{}, err
			}
		}
	}
}

// decodeSamples converts the raw data-chunk bytes described by h into
// per-channel float64 buffers. Stereo data decodes as-is; mono data is
// duplicated into two independent buffers so every downstream processor —
// all of which mutate left and right separately — behaves identically for
// mono and stereo input. This is the single decode switch for the whole
// package; ReadWAV, MeasureLUFS, Gain and readStereo all route through it.
func decodeSamples(h wavHeader, raw []byte) (left, right []float64, err error) {
	if h.numChannels == 1 {
		mono, err := decodeMono(h, raw)
		if err != nil {
			return nil, nil, err
		}
		right = append([]float64(nil), mono...)
		return mono, right, nil
	}
	switch {
	case h.audioFormat == 3 && h.bitsPerSample == 32:
		left, right = SamplesFromFloat32(raw)
	case h.audioFormat == 1 && h.bitsPerSample == 24:
		left, right = SamplesFromInt24(raw)
	case h.audioFormat == 1 && h.bitsPerSample == 32:
		left, right = SamplesFromInt32(raw)
	case h.audioFormat == 1 && h.bitsPerSample == 16:
		left, right = SamplesFromInt16(raw)
	default:
		return nil, nil, fmt.Errorf("format not supported: audioformat: %d, bits per sample: %d", h.audioFormat, h.bitsPerSample)
	}
	return left, right, nil
}

// decodeMono decodes single-channel PCM data into one float64 buffer,
// mirroring the format support of the stereo SamplesFrom* decoders.
func decodeMono(h wavHeader, raw []byte) ([]float64, error) {
	switch {
	case h.audioFormat == 3 && h.bitsPerSample == 32:
		n := len(raw) / 4
		out := make([]float64, n)
		for i := range n {
			out[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4 : i*4+4])))
		}
		return out, nil
	case h.audioFormat == 1 && h.bitsPerSample == 24:
		n := len(raw) / 3
		out := make([]float64, n)
		const scale = 1.0 / 8388608.0 // 2^23
		for i := range n {
			o := i * 3
			u := uint32(raw[o]) | uint32(raw[o+1])<<8 | uint32(raw[o+2])<<16
			s := int32(u)
			if u&0x800000 != 0 {
				s = int32(u | 0xFF000000)
			}
			out[i] = float64(s) * scale
		}
		return out, nil
	case h.audioFormat == 1 && h.bitsPerSample == 32:
		n := len(raw) / 4
		out := make([]float64, n)
		const scale = 1.0 / 2147483648.0 // 2^31
		for i := range n {
			out[i] = float64(int32(binary.LittleEndian.Uint32(raw[i*4:i*4+4]))) * scale
		}
		return out, nil
	case h.audioFormat == 1 && h.bitsPerSample == 16:
		n := len(raw) / 2
		out := make([]float64, n)
		const scale = 1.0 / 32768.0 // 2^15
		for i := range n {
			out[i] = float64(int16(binary.LittleEndian.Uint16(raw[i*2:i*2+2]))) * scale
		}
		return out, nil
	default:
		return nil, fmt.Errorf("format not supported: audioformat: %d, bits per sample: %d", h.audioFormat, h.bitsPerSample)
	}
}

// readSamples opens the WAV at path and returns decoded per-channel buffers
// plus the sample rate. Mono files come back as two duplicated buffers (see
// decodeSamples).
func readSamples(path string) (left, right []float64, sampleRate uint32, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, 0, err
	}
	defer f.Close()

	h, err := readWAVHeader(f)
	if err != nil {
		return nil, nil, 0, err
	}

	raw := make([]byte, h.dataSize)
	if _, err := io.ReadFull(f, raw); err != nil {
		return nil, nil, 0, err
	}
	left, right, err = decodeSamples(h, raw)
	if err != nil {
		return nil, nil, 0, err
	}
	return left, right, h.sampleRate, nil
}

// ReadStereoWAV reads the WAV at path into stereo float64 buffers and returns
// its sample rate. Mono input is duplicated into both channels. This is the
// exported entry point for callers that drive the *Samples processors
// directly (servers, batch pipelines) instead of the path-based wrappers.
func ReadStereoWAV(path string) (left, right []float64, sampleRate uint32, err error) {
	return readSamples(path)
}

func ReadWAV(path string) (left, right []float64, err error) {
	left, right, _, err = readSamples(path)
	return left, right, err
}
