package audio

// ReduceLRA shrinks the loudness range of the file at path toward targetLRA using
// a converging multipass: an eased downward compressor, an upward compressor, and
// a slow character limiter — all RMS-detecting and DS-driven. Each pass is gentle
// and keyed off the current overshoot, so the passes auto-taper and converge on
// target instead of overshooting. It stops at target, at maxPasses, or on
// diminishing returns (a pass buying < 0.3 LU). Returns the final LRA.
//
// This is the shared implementation used by both the app pipeline and the
// audition CLI, so they can never drift.
func ReduceLRA(path string, targetLRA float64, maxPasses int) (float64, error) {
	left, right, sampleRate, err := readSamples(path)
	if err != nil {
		return 0, err
	}
	lra := ReduceLRASamples(left, right, float64(sampleRate), targetLRA, maxPasses)
	if err := WriteFloat32WAV(path, sampleRate, left, right); err != nil {
		return lra, err
	}
	return lra, nil
}

// ReduceLRASamples is the in-memory core of ReduceLRA: it runs the same
// converging multipass directly on resident stereo buffers, so multi-pass
// processing costs one read and one write instead of ~ten file round-trips
// per pass. Returns the final LRA.
//
// The loop measures with loudnessRange directly — LRA is all it steers on,
// and skipping the integrated-loudness and true-peak legs of a full
// measurement saves most of each iteration's metering cost on long files.
func ReduceLRASamples(left, right []float64, sampleRate, targetLRA float64, maxPasses int) float64 {
	lra := loudnessRange(left, right, sampleRate)
	for pass := 0; pass < maxPasses && lra > targetLRA; pass++ {
		before := lra
		reduceLRAPass(left, right, sampleRate, lra-targetLRA)
		lra = loudnessRange(left, right, sampleRate)
		if before-lra < 0.3 {
			break // diminishing returns
		}
	}
	return lra
}

// reduceLRAPass applies one gentle pass of the LRA chain, sized to the
// current overshoot (over = current LRA − target, measured by the caller so
// it isn't re-measured here). Thresholds come from the RMS statistics; all
// time constants are DS-driven via GetCompressionModifiers.
func reduceLRAPass(left, right []float64, sampleRate, over float64) {
	if over <= 0 {
		return
	}

	ds := MeasureDynamicsScoreSamples(left, sampleRate)

	const wideKnee = 12.0
	const rmsWindowMs = 300.0 // RMS detection — track sustained loudness, not peaks
	mods := GetCompressionModifiers(ds.DynamicsScore)

	// Downward comp, eased — the upward stage shares the load.
	interp := clamp(0.6-over*0.05, 0.1, 0.6)
	thresh := ds.RMSLevel + interp*(ds.RMSPeak-ds.RMSLevel)
	ratio := clamp(1+over*0.15, 1, 6)
	CompressSamples(left, right, sampleRate, thresh, ratio, wideKnee,
		100*mods.AttackMultiplier, 1500*mods.ReleaseMultiplier, 0, rmsWindowMs)

	// Upward comp — lift passages below RMS level toward it.
	upRatio := clamp(1+over*0.1, 1, 4)
	const upMaxBoost = 5.0
	UpwardCompressSamples(left, right, sampleRate, ds.RMSLevel, upRatio, wideKnee,
		300*mods.AttackMultiplier, 600*mods.ReleaseMultiplier, upMaxBoost, rmsWindowMs)

	// Slow character limiter — threshold from the post-comp DS, placed inside the
	// RMS-to-RMSpeak span and pushed down the more dynamic the material is.
	ds2 := MeasureDynamicsScoreSamples(left, sampleRate)
	span := ds2.RMSPeak - ds2.RMSLevel
	dyn := clamp((ds2.DynamicsScore-9)/12, 0, 1)
	charThresh := ds2.RMSPeak - (0.2+0.3*dyn)*span
	mods2 := GetCompressionModifiers(ds2.DynamicsScore)
	CharacterLimitSamples(left, right, sampleRate, charThresh, 6,
		150*mods2.AttackMultiplier, 1200*mods2.ReleaseMultiplier)
}
