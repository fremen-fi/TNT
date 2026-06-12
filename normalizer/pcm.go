package normalizer

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
)

// riffSafeLimit is 4 GiB minus a small margin. A WAV dataSize at or above
// this value means ffmpeg clamped the RIFF uint32 — the file is truncated.
const riffSafeLimit = int64(0xFFFF0000)

// WAVInfo describes the PCM stream found in a WAV file.
type WAVInfo struct {
	SampleRate int
	Channels   int
	Format     uint16 // 1 = PCM int, 3 = IEEE float
	BitDepth   int
	DataSize   int64 // bytes of PCM data
}

// ReadWAVInfo reads the RIFF/WAVE header from r (which must support Seek).
// On success r is positioned at the first PCM sample byte.
// Returns an error if the data-chunk size is at the RIFF 4 GiB ceiling
// (indicating ffmpeg truncated a file that exceeded the limit).
func ReadWAVInfo(r io.ReadSeeker) (WAVInfo, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return WAVInfo{}, fmt.Errorf("read RIFF tag: %w", err)
	}
	if string(hdr[:]) != "RIFF" {
		return WAVInfo{}, fmt.Errorf("not a WAV file (expected RIFF, got %q)", string(hdr[:]))
	}
	var riffSize uint32
	if err := binary.Read(r, binary.LittleEndian, &riffSize); err != nil {
		return WAVInfo{}, err
	}
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return WAVInfo{}, fmt.Errorf("read WAVE tag: %w", err)
	}
	if string(hdr[:]) != "WAVE" {
		return WAVInfo{}, fmt.Errorf("not a WAV file (expected WAVE, got %q)", string(hdr[:]))
	}

	var info WAVInfo
	for {
		var chunkID [4]byte
		if _, err := io.ReadFull(r, chunkID[:]); err != nil {
			return WAVInfo{}, fmt.Errorf("read chunk id: %w", err)
		}
		var chunkSize uint32
		if err := binary.Read(r, binary.LittleEndian, &chunkSize); err != nil {
			return WAVInfo{}, err
		}
		switch string(chunkID[:]) {
		case "fmt ":
			if err := parseFmtChunk(r, chunkSize, &info); err != nil {
				return WAVInfo{}, err
			}
		case "data":
			if int64(chunkSize) >= riffSafeLimit {
				return WAVInfo{}, fmt.Errorf(
					"WAV data chunk size 0x%X is at RIFF 4 GiB ceiling — file is truncated",
					chunkSize,
				)
			}
			// Cross-check against actual file size when we have an *os.File.
			if f, ok := r.(*os.File); ok {
				stat, err := f.Stat()
				if err == nil {
					pos, _ := f.Seek(0, io.SeekCurrent)
					remaining := stat.Size() - pos
					if int64(chunkSize) > remaining+4 {
						return WAVInfo{}, fmt.Errorf(
							"WAV dataSize %d > actual remaining bytes %d — file truncated",
							chunkSize, remaining,
						)
					}
				}
			}
			info.DataSize = int64(chunkSize)
			return info, nil
		default:
			// Skip unknown or non-essential chunks (LIST, ID3, …).
			if _, err := io.CopyN(io.Discard, r, int64(chunkSize)); err != nil {
				return WAVInfo{}, err
			}
			if chunkSize%2 != 0 {
				r.Seek(1, io.SeekCurrent) //nolint:errcheck
			}
		}
	}
}

func parseFmtChunk(r io.Reader, size uint32, info *WAVInfo) error {
	var audioFmt uint16
	if err := binary.Read(r, binary.LittleEndian, &audioFmt); err != nil {
		return err
	}
	var channels uint16
	if err := binary.Read(r, binary.LittleEndian, &channels); err != nil {
		return err
	}
	var sampleRate uint32
	if err := binary.Read(r, binary.LittleEndian, &sampleRate); err != nil {
		return err
	}
	io.CopyN(io.Discard, r, 4) // byte rate (ignored)
	io.CopyN(io.Discard, r, 2) // block align (ignored)
	var bitDepth uint16
	if err := binary.Read(r, binary.LittleEndian, &bitDepth); err != nil {
		return err
	}
	// Skip any extra fmt bytes (extensible format, etc.)
	read := uint32(16)
	if size > read {
		io.CopyN(io.Discard, r, int64(size-read)) //nolint:errcheck
	}
	info.Format = audioFmt
	info.Channels = int(channels)
	info.SampleRate = int(sampleRate)
	info.BitDepth = int(bitDepth)
	return nil
}

// WriteWAVHeader writes a minimal RIFF/WAVE/fmt/data header for f32le stereo.
// dataSize is the number of PCM bytes that will follow; pass 0 to write a
// placeholder (the caller is responsible for seeking back and patching it).
func WriteWAVHeader(w io.Writer, sampleRate, channels int, dataSize int64) error {
	blockAlign := channels * 4 // f32 = 4 bytes per sample per channel
	byteRate := sampleRate * blockAlign

	riffSize := uint32(36 + dataSize) // 36 = fmt chunk (24) + data header (8)

	writes := []any{
		[]byte("RIFF"),
		riffSize,
		[]byte("WAVE"),
		[]byte("fmt "),
		uint32(18), // fmt chunk size (PCM would be 16; f32 needs extra 2 bytes)
		uint16(3),  // IEEE float
		uint16(channels),
		uint32(sampleRate),
		uint32(byteRate),
		uint16(blockAlign),
		uint16(32), // bit depth
		uint16(0),  // cbSize extra bytes (0 for non-extensible)
		[]byte("data"),
		uint32(dataSize),
	}
	for _, v := range writes {
		if err := binary.Write(w, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	return nil
}

// SamplesFromBytes returns the number of stereo frame pairs a WAVInfo's DataSize
// represents (DataSize / (channels * bytesPerSample)).
func (w WAVInfo) SampleFrames() int64 {
	bytesPerSample := int64(w.BitDepth / 8)
	if bytesPerSample == 0 {
		return 0
	}
	return w.DataSize / (int64(w.Channels) * bytesPerSample)
}

// readF32LE reads up to len(buf) interleaved float32 samples from r.
func readF32LE(r io.Reader, buf []float32) (int, error) {
	b := make([]byte, len(buf)*4)
	n, err := io.ReadFull(r, b)
	if err == io.ErrUnexpectedEOF {
		// Partial read at end of stream — convert what we got.
		n = n &^ 3 // round down to float32 boundary
		err = io.EOF
	}
	samples := n / 4
	for i := range samples {
		bits := binary.LittleEndian.Uint32(b[i*4:])
		buf[i] = math.Float32frombits(bits)
	}
	return samples, err
}

// writeF32LE writes interleaved float32 samples to w.
func writeF32LE(w io.Writer, buf []float32) error {
	b := make([]byte, len(buf)*4)
	for i, s := range buf {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(s))
	}
	_, err := w.Write(b)
	return err
}
