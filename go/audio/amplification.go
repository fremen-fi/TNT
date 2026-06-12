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
	left, right, sampleRate, err := readSamples(path)
	if err != nil {
		return fmt.Errorf("gain: %w", err)
	}
	GainSamples(left, right, offsetDB)
	return WriteFloat32WAV(path, sampleRate, left, right)
}

// GainSamples applies a linear gain of offsetDB decibels to both channels in
// place. It is the in-memory core of Gain.
func GainSamples(left, right []float64, offsetDB float64) {
	gain := math.Pow(10, offsetDB/20)
	for i := range left {
		left[i] *= gain
	}
	for i := range right {
		right[i] *= gain
	}
}

// WriteFloat32WAV writes stereo float64 samples to a 32-bit float WAV at path,
// atomically via a temp file in the same directory followed by a fsync and
// rename — the sync matters on servers, where a crash mid-pipeline must not
// leave a truncated file behind a completed rename.
func WriteFloat32WAV(path string, sampleRate uint32, left, right []float64) error {
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
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("syncing temp file: %w", err)
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

	// RIFF sizes are uint32: past 4 GiB the header silently wraps and every
	// downstream reader sees a corrupt file. Refuse instead — callers with
	// longer material must split or lower the intermediate sample rate.
	dataSize64 := uint64(nFrames) * uint64(blockAlign)
	if dataSize64+36 > math.MaxUint32 {
		return fmt.Errorf("WAV data would be %d bytes — over the 4 GiB RIFF limit", dataSize64)
	}
	dataSize := uint32(dataSize64)
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
