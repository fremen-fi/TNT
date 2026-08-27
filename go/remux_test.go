package main

import "testing"

func TestRemuxCompatible(t *testing.T) {
	cases := []struct {
		codec, ext string
		want       bool
	}{
		// PCM into MP4/MOV/MKV is fine, but not into the stricter
		// QuickTime-family and MPEG variants.
		{"PCM", ".mp4", true},
		{"PCM", ".mov", true},
		{"PCM", ".mkv", true},
		{"PCM", ".m4v", false},
		{"PCM", ".3gp", false},
		{"PCM", ".mpg", false},
		{"PCM", ".mpeg", false},

		// Opus is MP4/MKV/TS/WebM only.
		{"libopus", ".mp4", true},
		{"libopus", ".webm", true},
		{"libopus", ".mov", false},
		{"libopus", ".avi", false},
		{"libopus", ".flv", false},

		// FLAC works broadly except mov/webm/m4v/3gp/mpg/flv.
		{"flac", ".mp4", true},
		{"flac", ".mkv", true},
		{"flac", ".mov", false},
		{"flac", ".webm", false},
		{"flac", ".flv", false},

		// AAC is accepted almost everywhere, including webm's exception
		// list, which is opus/vorbis-only.
		{"libfdk_aac", ".mp4", true},
		{"aac_at", ".m4v", true},
		{"aac", ".3gp", true},
		{"libfdk_aac", ".webm", false},

		// MP3 covers the legacy mpg/mpeg program-stream muxer that
		// nothing else in our codec set is allowed into.
		{"libmp3lame", ".mpg", true},
		{"libmp3lame", ".mpeg", true},
		{"libmp3lame", ".webm", false},

		// Unrecognized codec/extension inputs are let through rather
		// than blocked.
		{"unknown_codec", ".mp4", true},
		{"PCM", ".unknownext", true},
	}
	for _, c := range cases {
		if got := remuxCompatible(c.codec, c.ext); got != c.want {
			t.Errorf("remuxCompatible(%q, %q) = %v, want %v", c.codec, c.ext, got, c.want)
		}
	}
}
