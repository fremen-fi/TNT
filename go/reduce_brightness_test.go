package main

import "testing"

func TestBrightnessStrength(t *testing.T) {
	cases := []struct {
		codec       string
		bitrateKbps int
		want        int
	}{
		{"libmp3lame", 320, 0},
		{"libmp3lame", 192, 1},
		{"libmp3lame", 160, 2},
		{"libmp3lame", 128, 3},
		{"libmp3lame", 96, 3},

		{"libfdk_aac", 192, 0},
		{"libfdk_aac", 128, 1},
		{"libfdk_aac", 96, 2},
		{"libfdk_aac", 64, 3},

		{"aac_at", 256, 0},
		{"aac_at", 128, 1},
		{"aac_at", 96, 2},
		{"aac_at", 64, 3},

		{"libopus", 128, 0},
		{"libopus", 96, 1},
		{"libopus", 64, 2},
		{"libopus", 48, 3},

		// Codecs without tiers never engage brightness reduction.
		{"PCM", 128, 0},
		{"flac", 128, 0},
		{"aac", 64, 0},
	}
	for _, c := range cases {
		if got := brightnessStrength(c.codec, c.bitrateKbps); got != c.want {
			t.Errorf("brightnessStrength(%q, %d) = %d, want %d", c.codec, c.bitrateKbps, got, c.want)
		}
	}
}

// Regression: processFile holds the bitrate in full bps for the
// needsFullNumber codecs (e.g. "128k" → 128000). The tier comparison used to
// run on that bps value against kbps tiers, so strength was always 0 and the
// brightness reduction / pre-limiter calibration never engaged for those
// codecs. The fix converts back to kbps before selecting the tier.
func TestBrightnessStrengthUsesKbpsNotBps(t *testing.T) {
	const bps = 128000
	if got := brightnessStrength("libmp3lame", bps/1000); got != 3 {
		t.Errorf("libmp3lame at 128k: strength = %d, want 3", got)
	}
	// The old bug: passing bps yields 0 because 128000 > every kbps tier.
	if got := brightnessStrength("libmp3lame", bps); got != 0 {
		t.Errorf("sanity: bps input should miss all tiers, got %d", got)
	}
}
