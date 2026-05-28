package config

import "testing"

func TestGetCodecKnown(t *testing.T) {
	cases := map[string]string{
		"Opus":       "libopus",
		"AAC":        "libfdk_aac",
		"MPEG-II L3": "libmp3lame",
		"PCM":        "PCM",
		"FLAC":       "flac",
	}
	for ui, want := range cases {
		if got := GetCodec(ui); got != want {
			t.Errorf("GetCodec(%q) = %q, want %q", ui, got, want)
		}
	}
}

func TestGetCodecUnknownReturnsInput(t *testing.T) {
	for _, in := range []string{"", "weird", "AAC (Fraunhofer)"} {
		if got := GetCodec(in); got != in {
			t.Errorf("GetCodec(%q) = %q, want passthrough", in, got)
		}
	}
}

func TestCodecMapHasRequiredEntries(t *testing.T) {
	required := []string{"Opus", "AAC", "MPEG-II L3", "PCM", "FLAC"}
	for _, k := range required {
		if _, ok := CodecMap[k]; !ok {
			t.Errorf("CodecMap missing required key %q", k)
		}
	}
}
