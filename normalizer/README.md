# `normalizer` — streaming DSP module

`github.com/fremen-fi/tnt/normalizer` — a self-contained, stdlib-only Go module
(own `go.mod`, no Wails, no GUI deps) holding the **streaming** version of TNT's
loudness + dynamics chain. Built for the **podcore** podcast transcoder, which
imports it (via a `replace` to this checkout; the TNT repo is public, so it can
also be imported by tag once pushed).

Everything is `io.Reader → io.Writer`: the whole audio file is never resident.
The processors are causal one-pole followers (`compressor`, `upwardCompressor`)
plus a bounded-lookahead true-peak limiter (`conformanceLimiter`), so processing
memory is **O(lookahead)** — tens of KiB at any duration. ffmpeg does no loudness
or dynamics work; it only decodes the source to a raw f32 PCM file and encodes
the result.

Public surface: `MeasureChain` (measure a PCM stream), `DeriveStage` (size one
adaptive stage from a measurement), `ApplyStage` (stream one stage), `Conform`
(gain-to-target + true-peak limit), plus `ChainMeasurement` / `StageParams`.

podcore drives a **converging multi-pass** with these, over a working PCM file on
disk (so RAM stays low while arbitrarily large PCM lives on disk):

```
decode → file ; measure
repeat: DeriveStage(measurement) → ApplyStage(file→file') → measure(file')
        until LRA ≤ target / diminishing returns / maxPasses
Conform(gain to -18 LUFS, true-peak limit to -5 dBTP) → file ; encode → AAC
```

Each pass re-reads the previous working file — the prior stages are already baked
in, so there is **no re-decode and nothing is AAC-encoded until the end**. On
Cloud Run the working file lives on a **gen2 ephemeral-disk volume** (not the
memory-backed filesystem), so the transcoder fits **1 vCPU / 1 GiB** at any
duration.

## ⚠️ This duplicates the DSP math in `../go/audio` — by design, with a catch

The per-sample math here is a **faithful port of the in-memory cores in
`go/audio`** (`processDynamics`, `UpwardCompressSamples`, `LookaheadLimitSamples`,
the BS.1770 meters, the `tpFIR` coefficients, the soft-knee gain curves). So the
same algorithms now live in **two places**:

- `go/audio` — operates on resident `[]float64` buffers. The **desktop (Wails)
  app** needs this: its editor/preview does random access over the whole file.
- `normalizer/` — operates on a stream. The **server** needs this: RAM stays
  bounded (the multi-GiB working PCM lives on the ephemeral disk, streamed a
  chunk at a time, never resident).

This is *not* the old cross-repo duplication (podcore once vendored a byte-copy
of `go/audio` at `internal/audio` — that's deleted). It's two implementations of
the same math in one repo, for two genuinely different access patterns.

**The catch — silent drift.** `normalizer/dynamics_test.go` verifies each
streaming processor against a *transcribed* whole-buffer reference (the `ref*`
functions in that test), **not** against `go/audio` directly. So if someone
changes a coefficient, threshold, or smoothing rule in `go/audio` and does **not**
mirror it here (and in the test's reference), the two engines diverge and nothing
fails. **When you touch the DSP math in `go/audio`, change it here too** (and the
test reference), or the desktop and server outputs will quietly differ.

## Path to a single source of truth (deferred)

The clean fix is to make `normalizer/` the *canonical* DSP and have `go/audio`'s
`*Samples` functions become thin wrappers that feed the whole buffer through the
streaming processors (a streaming processor run over an in-memory slice gives the
identical result — the streaming tests already prove that). Then there is one
implementation, and the in-memory API is just a convenience adapter.

That's a desktop-app refactor (its DSP call sites assume in-place `[]float64`
mutation), deferred for now. Until it happens, treat the two as a mirrored pair.

## Tuning note (podcore chain)

`DeriveStage` sizes ONE stage from a measurement, mirroring `go/audio.reduceLRAPass`.
The downward Compressor and Upward Compressor **share** the LRA reduction (lower
peaks, lift quiet passages), so the range shrinks from both ends with far less
gain reduction than downward alone — downward-only buys ~1-2 LU and costs
dynamics; the pair does much more.

This is the **real converging multi-pass**, the same feedback loop as the
in-memory `ReduceLRA`: each pass re-measures the working file and re-derives the
stage from the *current* (flattening) signal, so thresholds adapt and the loop
stops once it reaches the target or a pass buys < 0.3 LU. It lands precisely
(test: 15.6 LU → 6.8 LU in 3 passes, vs the earlier single-pass approximation
that stalled at 9.4). It costs extra disk passes, not re-decodes — the working
file is re-read, prior stages already baked in. Thresholds are a starting point;
dial them in against real spoken-word episodes.
