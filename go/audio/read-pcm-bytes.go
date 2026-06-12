package audio

import (
	"encoding/binary"
	"fmt"
	"io"
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

			if fmtChunk.NumChannels != 2 {
				return wavHeader{}, fmt.Errorf("is not stereo, got %d channels", fmtChunk.NumChannels)
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

func ReadWAV(path string) (left, right []float64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	h, err := readWAVHeader(f)
	if err != nil {
		return nil, nil, err
	}

	raw := make([]byte, h.dataSize)
	if _, err := io.ReadFull(f, raw); err != nil {
		return nil, nil, err
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
