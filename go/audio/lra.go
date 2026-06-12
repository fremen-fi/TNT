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
	r, err := MeasureLUFS(path)
	if err != nil {
		return 0, err
	}
	lra := r.LRA
	for pass := 0; pass < maxPasses && lra > targetLRA; pass++ {
		before := lra
		if err := reduceLRAPass(path, targetLRA); err != nil {
			return lra, err
		}
		if r, err = MeasureLUFS(path); err != nil {
			return lra, err
		}
		lra = r.LRA
		if before-lra < 0.3 {
			break // diminishing returns
		}
	}
	return lra, nil
}

// reduceLRAPass applies one gentle pass of the LRA chain, sized to the current
// overshoot. Thresholds come from the RMS statistics; all time constants are
// DS-driven via GetCompressionModifiers.
func reduceLRAPass(path string, targetLRA float64) error {
	m, err := MeasureLUFS(path)
	if err != nil {
		return err
	}
	over := m.LRA - targetLRA
	if over <= 0 {
		return nil
	}

	ds, err := MeasureDynamicsScore(path)
	if err != nil {
		return err
	}

	const wideKnee = 12.0
	const rmsWindowMs = 300.0 // RMS detection — track sustained loudness, not peaks
	mods := GetCompressionModifiers(ds.DynamicsScore)

	// Downward comp, eased — the upward stage shares the load.
	interp := clamp(0.6-over*0.05, 0.1, 0.6)
	thresh := ds.RMSLevel + interp*(ds.RMSPeak-ds.RMSLevel)
	ratio := clamp(1+over*0.15, 1, 6)
	if err := Compress(path, thresh, ratio, wideKnee,
		100*mods.AttackMultiplier, 1500*mods.ReleaseMultiplier, 0, rmsWindowMs); err != nil {
		return err
	}

	// Upward comp — lift passages below RMS level toward it.
	upRatio := clamp(1+over*0.1, 1, 4)
	const upMaxBoost = 5.0
	if err := UpwardCompress(path, ds.RMSLevel, upRatio, wideKnee,
		300*mods.AttackMultiplier, 600*mods.ReleaseMultiplier, upMaxBoost, rmsWindowMs); err != nil {
		return err
	}

	// Slow character limiter — threshold from the post-comp DS, placed inside the
	// RMS-to-RMSpeak span and pushed down the more dynamic the material is.
	ds2, err := MeasureDynamicsScore(path)
	if err != nil {
		return err
	}
	span := ds2.RMSPeak - ds2.RMSLevel
	dyn := clamp((ds2.DynamicsScore-9)/12, 0, 1)
	charThresh := ds2.RMSPeak - (0.2+0.3*dyn)*span
	mods2 := GetCompressionModifiers(ds2.DynamicsScore)
	return CharacterLimiter(path, charThresh, 6,
		150*mods2.AttackMultiplier, 1200*mods2.ReleaseMultiplier)
}
