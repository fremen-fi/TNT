package main

import (
	"fmt"

	"github.com/fremen-fi/tnt/go/audio"
)

// measureDynamicsScore computes the Dynamics Score natively (no ffmpeg) for the
// file at path. It mirrors calculateDynamicsScore's logging but reads the WAV
// directly via the audio package — the score is a time-domain statistic, so
// there is no reason to spawn ffmpeg astats for it.
func (n *AudioNormalizer) measureDynamicsScore(path string) *audio.DynamicsScoreAnalysis {
	result, err := audio.MeasureDynamicsScore(path)
	if err != nil {
		n.logFile.Write(fmt.Sprintf("Native DS calculation failed: %v", err))
		return nil
	}
	n.logFile.Write(fmt.Sprintf("DS Analysis (native) - RMS Peak: %.2f dB, RMS Level: %.2f dB, Crest: %.2f",
		result.RMSPeak, result.RMSLevel, result.CrestFactor))
	n.logFile.Write(fmt.Sprintf("Dynamics Score: %.2f", result.DynamicsScore))
	return result
}

// internalSampleRate returns the rate the processing chain runs at for a given
// final output rate. With a genuine true-peak limiter doing its own oversampled
// detection, the chain no longer needs heavy oversampling to approximate true
// peak — so this is just modest headroom for the EQ/dynamics: ×2 below 88.2 kHz
// (48k→96k, 44.1k→88.2k), and no resampling at 88.2 kHz and above.
func internalSampleRate(outputRate int) int {
	if outputRate <= 0 {
		return 96000
	}
	if outputRate < 88200 {
		return outputRate * 2
	}
	return outputRate
}

// Nominal compressor timings (ms) at normal dynamics, i.e. the values used when
// the Dynamics Score sits in the "normal" band where GetCompressionModifiers
// returns 1.0x. The DS modifiers scale these: a squashed file slows them down,
// a highly dynamic one speeds them up.
const (
	baseCompressorAttack  = 20.0
	baseCompressorRelease = 150.0
)

// compressorDynamics derives the compressor's full parameter set — ratio,
// attack, release and knee — for the file at path entirely from its Dynamics
// Score, exactly as calculateAdaptiveCompression does. The base ratio comes from
// the crest factor (audio.GetBaseRatioFromCrest) and the base timings from the
// nominal constants; both are then scaled by the DS-driven modifiers
// (audio.GetCompressionModifiers). The knee follows the resulting ratio
// (audio.GetKneeFromRatio). All values are clamped to the acompressor-valid
// ranges. If the DS measurement fails, gentle nominal defaults are used.
func (n *AudioNormalizer) compressorDynamics(path string) (ratio, attack, release, knee float64) {
	ratio = 1.4
	attack, release = baseCompressorAttack, baseCompressorRelease

	if ds := n.measureDynamicsScore(path); ds != nil {
		mods := audio.GetCompressionModifiers(ds.DynamicsScore)
		ratio = audio.GetBaseRatioFromCrest(ds.CrestFactor) * mods.RatioMultiplier
		attack *= mods.AttackMultiplier
		release *= mods.ReleaseMultiplier
		n.logFile.Write(fmt.Sprintf("DS Modifiers - Ratio: %.1fx, Attack: %.1fx, Release: %.1fx (DS %.2f, crest %.2f)",
			mods.RatioMultiplier, mods.AttackMultiplier, mods.ReleaseMultiplier, ds.DynamicsScore, ds.CrestFactor))
	}

	ratio = max(min(ratio, 20.0), 1.0)
	attack = max(min(attack, 2000.0), 0.01)
	release = max(min(release, 9000.0), 0.01)
	knee = audio.GetKneeFromRatio(ratio)

	n.logFile.Write(fmt.Sprintf("Compressor params - ratio %.1f:1, attack %.0f ms, release %.0f ms, knee %.1f dB",
		ratio, attack, release, knee))
	return ratio, attack, release, knee
}

// reduceLRA shrinks the loudness range toward targetLRA via the shared multipass
// chain (audio.ReduceLRA), logging the result. Self-gates: a no-op if LRA is
// already at/under target.
func (n *AudioNormalizer) reduceLRA(path string, targetLRA float64, passes int) {
	final, err := audio.ReduceLRA(path, targetLRA, passes)
	if err != nil {
		n.appLog.Write("There was an error in LRA reduction.")
		n.logFile.Write(fmt.Sprintf("LRA reduction failed: %v", err))
		return
	}
	n.logFile.Write(fmt.Sprintf("LRA reduced to %.1f (target %.1f)", final, targetLRA))
}

// truePeakCeilingMargin backs the conformance ceiling off by this many dB. The
// limiter is exact against its own (and ffmpeg's) meter on clean material, but
// upstream limiting adds HF that our 12-tap detection FIR under-reads by a steady
// ~0.1 dB versus a longer reference filter (ffmpeg / RTW). 0.2 dB keeps the
// reference reading safely under the requested ceiling — measured, not guessed
// (ceiling −1 → ffmpeg −0.9; ceiling −1.2 → ffmpeg −1.1). Verify on your meter and
// tighten toward 0.1 if you want to reclaim the headroom.
const truePeakCeilingMargin = 0.2

// conformLimit applies the true-peak conformance limiter at an explicit ceiling
// (dBFS), minus truePeakCeilingMargin. This is the always-on final guarantee —
// distinct from CharacterLimit, which uses the encoder-specific strength table.
func (n *AudioNormalizer) conformLimit(path string, ceilingDb float64) error {
	if err := audio.LookaheadLimiter(path, ceilingDb-truePeakCeilingMargin, 1, 30); err != nil {
		n.appLog.Write("There was an error in the conformance limiter.")
		n.logFile.Write(fmt.Sprintf("Conformance limiter failed: %v", err))
		return err
	}
	n.logFile.Write(fmt.Sprintf("Applied true-peak conformance limiter at %.1f dBFS to %s", ceilingDb, path))
	return nil
}

// ConformityLimit applies the lookahead brick-wall (conformity) limiter at the
// given calibration strength. The output is guaranteed never to exceed the
// limiter's ceiling, so this is the tool for forcing a file to conform to a TP
// target. The strength table is softLimiterCalibration (audio package).
func (n *AudioNormalizer) ConformityLimit(path string, strength int) error {
	if err := audio.LimitConforming(path, strength); err != nil {
		n.appLog.Write("There was an error applying the conformity limiter.")
		n.logFile.Write(fmt.Sprintf("Conformity limiter failed: %v", err))
		return err
	}
	n.logFile.Write(fmt.Sprintf("Applied conformity limiter (strength %d) to %s", strength, path))
	return nil
}

// CharacterLimit applies the soft-knee character limiter at the given
// calibration strength, sharing the same threshold/attack/release table as the
// conformity limiter but trading the brick wall for an audible, transient-led
// character.
func (n *AudioNormalizer) CharacterLimit(path string, strength int) error {
	if err := audio.LimitCharacter(path, strength); err != nil {
		n.appLog.Write("There was an error applying the character limiter.")
		n.logFile.Write(fmt.Sprintf("Character limiter failed: %v", err))
		return err
	}
	n.logFile.Write(fmt.Sprintf("Applied character limiter (strength %d) to %s", strength, path))
	return nil
}

// Compress applies the feed-forward compressor with caller-supplied controls:
// thresholdDb, ratio (>= 1), kneeDb, the attackMs / releaseMs time factors,
// makeupDb of output gain, and rmsMs (0 = peak detection, >0 = RMS detection with
// that window — use RMS for loudness/LRA leveling).
func (n *AudioNormalizer) Compress(path string, thresholdDb, ratio, kneeDb, attackMs, releaseMs, makeupDb, rmsMs float64) error {
	if err := audio.Compress(path, thresholdDb, ratio, kneeDb, attackMs, releaseMs, makeupDb, rmsMs); err != nil {
		n.appLog.Write("There was an error applying compression.")
		n.logFile.Write(fmt.Sprintf("Compression failed: %v", err))
		return err
	}
	n.logFile.Write(fmt.Sprintf("Applied compression (thr %.1f dB, ratio %.1f:1, knee %.1f dB, atk %.0f ms, rel %.0f ms, makeup %.1f dB) to %s",
		thresholdDb, ratio, kneeDb, attackMs, releaseMs, makeupDb, path))
	return nil
}
