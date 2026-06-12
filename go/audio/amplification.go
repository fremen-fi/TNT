package audio

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

// Gain applies a linear gain of offsetDB decibels to every sample of the
// stereo WAV at path, rewriting the file in place as a 32-bit float WAV
// (pcm_f32le, audioFormat=3). The write is atomic: samples are written to a
// temp file in the same directory and then renamed over the original.
func Gain(path string, offsetDB float64) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}

	h, err := readWAVHeader(f)
	if err != nil {
		f.Close()
		return fmt.Errorf("reading WAV header: %w", err)
	}

	raw := make([]byte, h.dataSize)
	if _, err := io.ReadFull(f, raw); err != nil {
		f.Close()
		return fmt.Errorf("reading PCM data: %w", err)
	}
	f.Close()

	var left, right []float64
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
		return fmt.Errorf("unsupported format: audioformat=%d bits=%d", h.audioFormat, h.bitsPerSample)
	}

	gain := math.Pow(10, offsetDB/20)
	for i := range left {
		left[i] *= gain
		right[i] *= gain
	}

	return writeFloat32WAV(path, h.sampleRate, left, right)
}

// writeFloat32WAV writes stereo float64 samples to a 32-bit float WAV at path,
// atomically via a temp file in the same directory followed by a rename.
func writeFloat32WAV(path string, sampleRate uint32, left, right []float64) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".gain-*.wav.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()

	if err := writeFloat32WAVTo(tmp, sampleRate, left, right); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming temp file over %s: %w", path, err)
	}
	return nil
}

func writeFloat32WAVTo(w io.Writer, sampleRate uint32, left, right []float64) error {
	const (
		numChannels   = 2
		bitsPerSample = 32
		audioFormat   = 3 // IEEE float
	)
	nFrames := len(left)
	nFrames = min(len(right), nFrames)

	blockAlign := uint16(numChannels * bitsPerSample / 8)
	byteRate := sampleRate * uint32(blockAlign)
	dataSize := uint32(nFrames) * uint32(blockAlign)
	riffSize := 36 + dataSize

	// Buffer the writer: every header field and sample is emitted through bw as a
	// tiny 2–4 byte write, so without buffering a long file turns into hundreds of
	// millions of one-sample syscalls — slow enough to look like a hang at high
	// internal sample rates (e.g. ~174M writes for a 4-minute 384 kHz stereo file).
	buf := bufio.NewWriterSize(w, 1<<20)
	bw := newByteWriter(buf)

	// RIFF header
	bw.bytes([]byte("RIFF"))
	bw.u32(riffSize)
	bw.bytes([]byte("WAVE"))

	// fmt chunk
	bw.bytes([]byte("fmt "))
	bw.u32(16)
	bw.u16(audioFormat)
	bw.u16(numChannels)
	bw.u32(sampleRate)
	bw.u32(byteRate)
	bw.u16(blockAlign)
	bw.u16(bitsPerSample)

	// data chunk
	bw.bytes([]byte("data"))
	bw.u32(dataSize)

	for i := 0; i < nFrames; i++ {
		bw.u32(math.Float32bits(float32(left[i])))
		bw.u32(math.Float32bits(float32(right[i])))
	}

	if bw.err != nil {
		return bw.err
	}
	return buf.Flush()
}

// byteWriter is a tiny little-endian helper that defers error checking so the
// header/sample emission above reads cleanly.
type byteWriter struct {
	w   io.Writer
	buf [4]byte
	err error
}

func newByteWriter(w io.Writer) *byteWriter { return &byteWriter{w: w} }

func (b *byteWriter) bytes(p []byte) {
	if b.err != nil {
		return
	}
	_, b.err = b.w.Write(p)
}

func (b *byteWriter) u16(v uint16) {
	if b.err != nil {
		return
	}
	binary.LittleEndian.PutUint16(b.buf[:2], v)
	_, b.err = b.w.Write(b.buf[:2])
}

func (b *byteWriter) u32(v uint32) {
	if b.err != nil {
		return
	}
	binary.LittleEndian.PutUint32(b.buf[:4], v)
	_, b.err = b.w.Write(b.buf[:4])
}
