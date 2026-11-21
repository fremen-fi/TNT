package normalizer

// Real-time, block-based wrappers around the streaming dynamics engines in
// dynamics.go. These add no DSP of their own: Compressor wraps `compressor`,
// UpwardCompressor wraps `upwardCompressor` and Limiter wraps
// `conformanceLimiter`, so the per-sample math (compressor.step, the true-peak
// detector, the windowed-minimum gain) has exactly one implementation shared with
// the podcore ApplyStage / Conform paths.
//
// Contract (external callers rely on this):
//
//   - Process / ProcessMono / SetParams / SetCeiling / SetRelease / Drain perform
//     ZERO heap allocations; every buffer is sized once at construction.
//   - Not goroutine-safe; the caller serializes all calls on one value.
//   - Parameters change at runtime without clearing detector or gain state, so
//     the one-pole smoothing absorbs the change. MakeupDb is slewed (10 ms
//     one-pole time constant; ~0.3 s to settle exactly) for the same reason.
//   - NaN / ±Inf input samples are replaced by 0 on entry and never reach state.
//   - Every constructor argument and setter is clamped to a sane finite range; a
//     NaN passed to a setter leaves that parameter unchanged.
//   - Output is written in place, one output frame per input frame. If l and r
//     differ in length only the common prefix is processed and the excess of the
//     longer slice is zeroed (unprocessed audio is never passed through).
//   - Mono behaves bit-identically to stereo with L == R.
//
// Limiter latency: the limiter delays audio by LatencySamples() frames. During
// the first LatencySamples() frames of a stream Process emits silence (there is
// no audio to emit yet), so output length always equals input length and a
// signal fed at frame n appears at frame n+LatencySamples(). The final
// LatencySamples() frames of audio are still inside the limiter when the input
// ends; Drain retrieves them. Drain ends the stream: call Reset before feeding
// new audio (continuing after a Drain restarts the warm-up silence).
//
// Minimum lookahead: the 12-tap true-peak detector only sees a sample's
// reconstructed peak 5-11 frames after the sample arrives, so the limiter needs
// at least 12 frames of lookahead to catch it in time. NewLimiter raises shorter
// requests to 12 frames (0.25 ms at 48 kHz); check LatencySamples() for the
// effective value.

import "math"

// sanitizeSample maps NaN and ±Inf to 0.
func sanitizeSample(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	return x
}

// sane clamps x to [lo, hi], substituting def for NaN. lo/hi may be infinite.
func sane(x, lo, hi, def float64) float64 {
	if math.IsNaN(x) {
		return def
	}
	return clampf(x, lo, hi)
}

const (
	maxTimeMs      = 60000.0
	maxKneeDb      = 96.0
	minThresholdDb = -150.0
	maxThresholdDb = 30.0
	maxMakeupDb    = 60.0
	maxBoostLimit  = 60.0
	minCeilingDb   = -60.0

	minSampleRate = 1000.0
	maxSampleRate = 1e6

	makeupSlewMs        = 10.0
	minLimiterLookahead = len(tpFIR[0]) // frames; see "Minimum lookahead" above
)

func saneSampleRate(sr float64) float64 { return sane(sr, minSampleRate, maxSampleRate, 48000) }

// zeroTail zeroes whatever part of the longer of l, r lies beyond n frames.
func zeroTail(l, r []float64, n int) {
	for i := n; i < len(l); i++ {
		l[i] = 0
	}
	for i := n; i < len(r); i++ {
		r[i] = 0
	}
}

// ── Compressor ──────────────────────────────────────────────────────────────

// CompressorParams are the runtime-adjustable settings of a Compressor.
type CompressorParams struct {
	ThresholdDb float64 // dBFS above which gain reduction starts
	Ratio       float64 // >= 1; +Inf makes it a limiter
	KneeDb      float64 // soft-knee width, >= 0
	AttackMs    float64 // gain-reduction attack time constant, >= 0 (0 = instant)
	ReleaseMs   float64 // gain-recovery release time constant, >= 0 (0 = instant)
	MakeupDb    float64 // post-gain; changes are slewed over ~10 ms
	RMSWindowMs float64 // > 0 selects a one-pole RMS detector; 0 selects peak
}

// sanitized clamps p; a NaN field takes its value from def.
func (p CompressorParams) sanitized(def CompressorParams) CompressorParams {
	return CompressorParams{
		ThresholdDb: sane(p.ThresholdDb, minThresholdDb, maxThresholdDb, def.ThresholdDb),
		Ratio:       sane(p.Ratio, 1, math.Inf(1), def.Ratio),
		KneeDb:      sane(p.KneeDb, 0, maxKneeDb, def.KneeDb),
		AttackMs:    sane(p.AttackMs, 0, maxTimeMs, def.AttackMs),
		ReleaseMs:   sane(p.ReleaseMs, 0, maxTimeMs, def.ReleaseMs),
		MakeupDb:    sane(p.MakeupDb, -maxMakeupDb, maxMakeupDb, def.MakeupDb),
		RMSWindowMs: sane(p.RMSWindowMs, 0, maxTimeMs, def.RMSWindowMs),
	}
}

// Compressor is a real-time stereo-linked feed-forward compressor built on the
// same engine as the podcore chain.
type Compressor struct {
	eng        *compressor
	sampleRate float64
	p          CompressorParams // current (clamped) parameters

	makeupTarget float64 // linear; eng.makeup slews toward it
	makeupCoef   float64
}

// NewCompressor builds a compressor for sampleRate with parameters p (clamped).
func NewCompressor(sampleRate float64, p CompressorParams) *Compressor {
	sampleRate = saneSampleRate(sampleRate)
	p = p.sanitized(CompressorParams{Ratio: 1})
	eng := newCompressor(sampleRate, p.ThresholdDb, p.Ratio, p.KneeDb,
		p.AttackMs, p.ReleaseMs, p.MakeupDb, p.RMSWindowMs)
	return &Compressor{
		eng:          eng,
		sampleRate:   sampleRate,
		p:            p,
		makeupTarget: eng.makeup,
		makeupCoef:   onePoleCoef(makeupSlewMs, sampleRate),
	}
}

// SetParams applies p (clamped; NaN fields keep their current value) without
// touching detector or gain state.
func (c *Compressor) SetParams(p CompressorParams) {
	p = p.sanitized(c.p)
	c.p = p
	e := c.eng
	e.attCoef = onePoleCoef(p.AttackMs, c.sampleRate)
	e.relCoef = onePoleCoef(p.ReleaseMs, c.sampleRate)
	e.rmsCoef = onePoleCoef(p.RMSWindowMs, c.sampleRate)
	if p.RMSWindowMs > 0 && !e.useRMS {
		e.meanSq = 0 // not tracked in peak mode; don't start from a stale value
	}
	e.useRMS = p.RMSWindowMs > 0
	c.makeupTarget = dbToLin(p.MakeupDb)
	e.thresholdDb = p.ThresholdDb
	e.ratio = p.Ratio
	e.kneeDb = p.KneeDb
}

func (c *Compressor) step(l, r float64) (float64, float64) {
	if e := c.eng; e.makeup != c.makeupTarget {
		e.makeup += (c.makeupTarget - e.makeup) * (1 - c.makeupCoef)
		if math.Abs(c.makeupTarget-e.makeup) <= 1e-9*c.makeupTarget {
			e.makeup = c.makeupTarget
		}
	}
	return c.eng.step(sanitizeSample(l), sanitizeSample(r))
}

// Process compresses l and r in place. See the package notes for unequal lengths.
func (c *Compressor) Process(l, r []float64) {
	n := min(len(l), len(r))
	for i := 0; i < n; i++ {
		l[i], r[i] = c.step(l[i], r[i])
	}
	zeroTail(l, r, n)
}

// ProcessMono compresses x in place; identical to Process with L == R.
func (c *Compressor) ProcessMono(x []float64) {
	for i := range x {
		v := sanitizeSample(x[i])
		x[i], _ = c.step(v, v)
	}
}

// Reset clears detector and gain state; parameters are kept.
func (c *Compressor) Reset() {
	c.eng.meanSq = 0
	c.eng.gain = 1.0
	c.eng.makeup = c.makeupTarget
}

// GainReductionDB returns the current gain reduction in dB (>= 0), for metering.
func (c *Compressor) GainReductionDB() float64 { return 0 - linToDb(c.eng.gain) }

// ── Upward compressor ───────────────────────────────────────────────────────

// UpwardCompressorParams are the runtime-adjustable settings of an
// UpwardCompressor.
type UpwardCompressorParams struct {
	ThresholdDb float64 // dBFS below which passages are lifted
	Ratio       float64 // >= 1
	KneeDb      float64 // soft-knee width, >= 0
	AttackMs    float64 // boost attack time constant, >= 0
	ReleaseMs   float64 // boost release time constant, >= 0
	MaxBoostDb  float64 // cap on the boost, >= 0
	RMSWindowMs float64 // > 0 selects a one-pole RMS detector; 0 selects peak
}

func (p UpwardCompressorParams) sanitized(def UpwardCompressorParams) UpwardCompressorParams {
	return UpwardCompressorParams{
		ThresholdDb: sane(p.ThresholdDb, minThresholdDb, maxThresholdDb, def.ThresholdDb),
		Ratio:       sane(p.Ratio, 1, math.Inf(1), def.Ratio),
		KneeDb:      sane(p.KneeDb, 0, maxKneeDb, def.KneeDb),
		AttackMs:    sane(p.AttackMs, 0, maxTimeMs, def.AttackMs),
		ReleaseMs:   sane(p.ReleaseMs, 0, maxTimeMs, def.ReleaseMs),
		MaxBoostDb:  sane(p.MaxBoostDb, 0, maxBoostLimit, def.MaxBoostDb),
		RMSWindowMs: sane(p.RMSWindowMs, 0, maxTimeMs, def.RMSWindowMs),
	}
}

// UpwardCompressor is the real-time form of the podcore upward compressor.
type UpwardCompressor struct {
	eng        *upwardCompressor
	sampleRate float64
	p          UpwardCompressorParams
}

// NewUpwardCompressor builds an upward compressor for sampleRate (p clamped).
func NewUpwardCompressor(sampleRate float64, p UpwardCompressorParams) *UpwardCompressor {
	sampleRate = saneSampleRate(sampleRate)
	p = p.sanitized(UpwardCompressorParams{Ratio: 1})
	return &UpwardCompressor{
		eng: newUpwardCompressor(sampleRate, p.ThresholdDb, p.Ratio, p.KneeDb,
			p.AttackMs, p.ReleaseMs, p.MaxBoostDb, p.RMSWindowMs),
		sampleRate: sampleRate,
		p:          p,
	}
}

// SetParams applies p (clamped; NaN fields keep their current value) without
// touching detector or gain state.
func (u *UpwardCompressor) SetParams(p UpwardCompressorParams) {
	p = p.sanitized(u.p)
	u.p = p
	e := u.eng
	e.attCoef = onePoleCoef(p.AttackMs, u.sampleRate)
	e.relCoef = onePoleCoef(p.ReleaseMs, u.sampleRate)
	e.rmsCoef = onePoleCoef(p.RMSWindowMs, u.sampleRate)
	if p.RMSWindowMs > 0 && !e.useRMS {
		e.meanSq = 0
	}
	e.useRMS = p.RMSWindowMs > 0
	e.thresholdDb = p.ThresholdDb
	e.ratio = p.Ratio
	e.kneeDb = p.KneeDb
	e.maxBoostDb = p.MaxBoostDb
}

// Process lifts l and r in place. See the package notes for unequal lengths.
func (u *UpwardCompressor) Process(l, r []float64) {
	n := min(len(l), len(r))
	for i := 0; i < n; i++ {
		l[i], r[i] = u.eng.step(sanitizeSample(l[i]), sanitizeSample(r[i]))
	}
	zeroTail(l, r, n)
}

// ProcessMono lifts x in place; identical to Process with L == R.
func (u *UpwardCompressor) ProcessMono(x []float64) {
	for i := range x {
		v := sanitizeSample(x[i])
		x[i], _ = u.eng.step(v, v)
	}
}

// Reset clears detector and gain state; parameters are kept.
func (u *UpwardCompressor) Reset() {
	u.eng.meanSq = 0
	u.eng.gain = 1.0
}

// BoostDB returns the current boost in dB (>= 0), for metering.
func (u *UpwardCompressor) BoostDB() float64 { return linToDb(u.eng.gain) }

// ── True-peak limiter ───────────────────────────────────────────────────────

// Limiter is a real-time true-peak brick-wall limiter: BS.1770 4x polyphase
// detection, sliding-window-minimum lookahead, instant attack and one-pole
// release, with a hard clamp at the ceiling as backstop. See the package notes
// above for the latency / warm-up contract.
type Limiter struct {
	eng        *conformanceLimiter
	sampleRate float64
	ceilingDB  float64
	releaseMs  float64
}

func saneCeilingDB(db, def float64) float64 { return sane(db, minCeilingDb, 0, def) }
func saneReleaseMs(ms, def float64) float64 { return sane(ms, 0, maxTimeMs, def) }

// NewLimiter builds a limiter. lookaheadMs is fixed for the life of the value
// (it sizes the ring buffers and sets the latency) and is raised to at least 12
// frames; ceiling and release can be changed later with SetCeiling / SetRelease.
func NewLimiter(sampleRate, ceilingDB, lookaheadMs, releaseMs float64) *Limiter {
	sampleRate = saneSampleRate(sampleRate)
	ceilingDB = saneCeilingDB(ceilingDB, 0)
	releaseMs = saneReleaseMs(releaseMs, 0)
	lookaheadMs = sane(lookaheadMs, 0, 1000, 20)
	if floor := float64(minLimiterLookahead) / sampleRate * 1000; lookaheadMs < floor {
		lookaheadMs = floor
	}
	return &Limiter{
		eng:        newConformanceLimiter(sampleRate, ceilingDB, lookaheadMs, releaseMs),
		sampleRate: sampleRate,
		ceilingDB:  ceilingDB,
		releaseMs:  releaseMs,
	}
}

// SetCeiling changes the ceiling (dBFS, clamped to [-60, 0]; NaN is ignored).
// Frames already inside the lookahead are re-limited against the new ceiling
// from the next output frame (attack is instant); the hard clamp remains only
// as a sample-peak backstop. Setting the current value is a no-op.
func (l *Limiter) SetCeiling(ceilingDB float64) {
	l.ceilingDB = saneCeilingDB(ceilingDB, l.ceilingDB)
	l.eng.setThresh(dbToLin(l.ceilingDB))
}

// SetRelease changes the gain-release time constant (ms, clamped to [0, 60000];
// NaN is ignored).
func (l *Limiter) SetRelease(releaseMs float64) {
	l.releaseMs = saneReleaseMs(releaseMs, l.releaseMs)
	l.eng.relCoef = onePoleCoef(l.releaseMs, l.sampleRate)
}

// LatencySamples is the fixed delay, in frames, between input and output.
func (l *Limiter) LatencySamples() int { return l.eng.look }

// Process limits l and r in place. The first LatencySamples() frames of a stream
// come out as silence. See the package notes for unequal lengths.
func (l *Limiter) Process(left, right []float64) {
	n := min(len(left), len(right))
	for i := 0; i < n; i++ {
		ol, or, ok := l.eng.push(sanitizeSample(left[i]), sanitizeSample(right[i]))
		if !ok {
			ol, or = 0, 0
		}
		left[i], right[i] = ol, or
	}
	zeroTail(left, right, n)
}

// ProcessMono limits x in place; identical to Process with L == R.
func (l *Limiter) ProcessMono(x []float64) {
	for i := range x {
		v := sanitizeSample(x[i])
		ol, _, ok := l.eng.push(v, v)
		if !ok {
			ol = 0
		}
		x[i] = ol
	}
}

// Pending is the number of frames still inside the limiter that Drain would
// emit: LatencySamples() once warmed up, fewer before that.
func (l *Limiter) Pending() int { return l.eng.inIdx - l.eng.emitIdx }

// Drain emits up to min(len(left), len(right)) of the frames still held in the
// lookahead (their windows truncate at end of stream, exactly like Conform's
// flush) and returns how many it wrote. Call repeatedly until it returns 0 or
// Pending() == 0. Drain ends the stream; call Reset before reusing the limiter.
func (l *Limiter) Drain(left, right []float64) int {
	n := min(len(left), len(right))
	e := l.eng
	i := 0
	for ; i < n && e.emitIdx < e.inIdx; i++ {
		left[i], right[i] = e.emitTail()
	}
	return i
}

// DrainMono is Drain for a mono stream.
func (l *Limiter) DrainMono(x []float64) int {
	e := l.eng
	i := 0
	for ; i < len(x) && e.emitIdx < e.inIdx; i++ {
		x[i], _ = e.emitTail()
	}
	return i
}

// Reset clears all audio and gain state; ceiling, release and lookahead are kept.
func (l *Limiter) Reset() { l.eng.reset() }
