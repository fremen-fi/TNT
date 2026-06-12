// Package normalizer implements streaming EBU R128 loudness normalization for
// PCM WAV audio. All processing uses io.Reader / io.Writer so the entire audio
// file is never held in memory; memory use is O(duration/100ms) for loudness
// gate block energies, typically < 1 MiB even for a 3-hour file.
//
// Intended for use with podcore's Cloud Run Job transcoder. The caller decodes
// audio to an f32le stereo WAV with FFmpeg, then calls Normalize which:
//
//  1. Reads the WAV header and validates the RIFF data-chunk size against the
//     actual file size (guards against ffmpeg's silent 4 GiB RIFF wrap).
//  2. Performs a single streaming EBU R128 measurement pass to obtain
//     integrated LUFS, LRA, and sample peak.
//  3. Computes the linear gain required to reach TargetLUFS.
//  4. Performs a second streaming pass applying that gain and hard-clipping at
//     SamplePeakCeiling (not true-peak; the encode pass handles TP via loudnorm
//     linear mode + alimiter).
//
// The output is a new f32le stereo WAV that the caller passes to the encode
// stage.
package normalizer

import (
	"fmt"
	"io"
	"math"
	"os"
)

// Config holds normalization parameters.
type Config struct {
	// TargetLUFS is the desired integrated loudness, e.g. -18.0.
	TargetLUFS float64

	// SamplePeakCeiling is the maximum allowed |sample| value (linear).
	// Use math.Pow(10, dBTP/20) — e.g. 0.5623 for -5 dBFS.
	SamplePeakCeiling float64

	// MaxLRA, when > 0, clamps the measured LRA fed to the caller. When the
	// input was pre-processed by a dynamics stage the caller should set this to
	// the LRA target (e.g. 11) so that loudnorm linear mode is always selected.
	// Pass 0 to return the raw measured value.
	MaxLRA float64
}

// Result carries the measurements and actions taken by Normalize.
type Result struct {
	MeasuredLUFS   float64
	MeasuredLRA    float64 // raw measured (before MaxLRA clamping)
	ClampedLRA     float64 // value fed back to encode stage (clamped if MaxLRA > 0)
	MeasuredPeakDB float64 // sample peak before gain, dBFS
	AppliedGainDB  float64
	Clipped        bool // any sample was at SamplePeakCeiling after gain
}

// Normalize reads the WAV at srcPath, normalizes it to cfg.TargetLUFS and
// writes the result to dstPath (overwritten or created). Both paths should
// reference ephemeral disk locations to avoid RAM pressure.
//
// The function is intentionally two-pass (measure then apply) so it can run on
// arbitrarily long files with bounded memory. An ephemeral disk write for the
// output is the only I/O amplification.
func Normalize(srcPath, dstPath string, cfg Config) (Result, error) {
	// ── Pass 1: measure ──────────────────────────────────────────────────────
	src, err := os.Open(srcPath)
	if err != nil {
		return Result{}, fmt.Errorf("open src: %w", err)
	}
	defer src.Close()

	info, err := ReadWAVInfo(src)
	if err != nil {
		return Result{}, fmt.Errorf("read WAV header: %w", err)
	}
	if info.Channels != 2 {
		return Result{}, fmt.Errorf("expected stereo (2-ch) WAV, got %d channels", info.Channels)
	}

	meas, err := MeasureLUFS(src, info.SampleRate, info.DataSize)
	if err != nil {
		return Result{}, fmt.Errorf("measure LUFS: %w", err)
	}

	gainDB := cfg.TargetLUFS - meas.IntegratedLUFS
	gainLinear := math.Pow(10, gainDB/20)

	clampedLRA := meas.LRA
	if cfg.MaxLRA > 0 && clampedLRA > cfg.MaxLRA {
		clampedLRA = cfg.MaxLRA
	}

	// ── Pass 2: apply gain + sample-peak limit ───────────────────────────────
	// Seek src back to start of PCM data.
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return Result{}, err
	}
	if _, err := ReadWAVInfo(src); err != nil {
		return Result{}, fmt.Errorf("re-read header for pass 2: %w", err)
	}

	dst, err := os.Create(dstPath)
	if err != nil {
		return Result{}, fmt.Errorf("create dst: %w", err)
	}
	defer dst.Close()

	if err := WriteWAVHeader(dst, info.SampleRate, info.Channels, info.DataSize); err != nil {
		return Result{}, fmt.Errorf("write WAV header: %w", err)
	}

	_, postGainPeakDB, err := ApplyGainAndLimit(src, dst, gainLinear, cfg.SamplePeakCeiling)
	if err != nil {
		return Result{}, fmt.Errorf("apply gain: %w", err)
	}

	// postGainPeakDB is peak AFTER gain, before hard-clip.
	clipped := postGainPeakDB > linearToDBFS(cfg.SamplePeakCeiling)

	return Result{
		MeasuredLUFS:   meas.IntegratedLUFS,
		MeasuredLRA:    meas.LRA,
		ClampedLRA:     clampedLRA,
		MeasuredPeakDB: meas.SamplePeakDB,
		AppliedGainDB:  gainDB,
		Clipped:        clipped,
	}, nil
}

func linearToDBFS(linear float64) float64 {
	if linear <= 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(linear)
}
