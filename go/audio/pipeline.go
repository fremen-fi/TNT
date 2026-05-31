package audio

import "strings"

// FilterChain accumulates ffmpeg audio-filter expressions across pipeline
// stages so the whole graph can be applied in a single ffmpeg invocation —
// no intermediate WAV files between stages.
//
// Each Add appends one filter expression, comma-joined to whatever came
// before. The first non-empty Add also injects an aresample=192000 hop
// right after that first filter so every subsequent stage operates at
// 192 kHz, matching the precision the prior temp-file pipeline got from
// writing pcm_f64le WAVs at 192 kHz between stages. Empty additions are
// skipped (and don't trigger the upsample), so absent stages contribute
// nothing.
type FilterChain struct {
	parts     []string
	upsampled bool
}

// Add appends a filter expression to the chain. Empty strings are no-ops.
// On the first non-empty Add, the 192 kHz upsample is inserted directly
// after the added filter.
func (c *FilterChain) Add(filter string) {
	if filter == "" {
		return
	}
	c.parts = append(c.parts, filter)
	if !c.upsampled {
		c.parts = append(c.parts, "aresample=192000")
		c.upsampled = true
	}
}

// String returns the comma-joined filter graph, or "" if nothing was added.
func (c *FilterChain) String() string {
	if len(c.parts) == 0 {
		return ""
	}
	return strings.Join(c.parts, ",")
}

// IsEmpty reports whether anything has been added.
func (c *FilterChain) IsEmpty() bool {
	return len(c.parts) == 0
}

// Prefix returns the current chain prefixed with a comma-joined suffix, or
// just the suffix if the chain is empty. Useful when an analysis pass wants
// to run "<accumulated chain>,<analysis filter>" without mutating the chain.
func (c *FilterChain) Prefix(suffix string) string {
	if c.IsEmpty() {
		return suffix
	}
	if suffix == "" {
		return c.String()
	}
	return c.String() + "," + suffix
}
