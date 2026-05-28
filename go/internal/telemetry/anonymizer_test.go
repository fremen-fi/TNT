package telemetry

import (
	"strings"
	"testing"
)

func TestScrubArgs_PathReplaced(t *testing.T) {
	in := []string{"-i", "/Users/mika/Music/song.wav", "-c:a", "flac"}
	got := ScrubArgs(in)
	want := []string{"-i", "<path>/<file>.wav", "-c:a", "flac"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("arg %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestScrubArgs_WindowsPath(t *testing.T) {
	in := []string{"-i", `C:\Users\mika\My Music\song.mp3`}
	got := ScrubArgs(in)
	if got[1] != "<path>/<file>.mp3" {
		t.Errorf("windows path: got %q", got[1])
	}
}

func TestScrubArgs_MetadataRedacted(t *testing.T) {
	in := []string{"-metadata", "title=My Secret Song", "-metadata", "artist=Some Artist"}
	got := ScrubArgs(in)
	want := []string{"-metadata", "title=<redacted>", "-metadata", "artist=<redacted>"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("arg %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestScrubArgs_FilterGraphPreserved(t *testing.T) {
	// Filter graphs are signal — must not be touched.
	in := []string{"-af", "loudnorm=I=-23:TP=-1:LRA=11"}
	got := ScrubArgs(in)
	if got[1] != in[1] {
		t.Errorf("filter graph mangled: got %q", got[1])
	}
}

func TestScrubArgs_FlagsPreserved(t *testing.T) {
	in := []string{"-y", "-hide_banner", "-loglevel", "error"}
	got := ScrubArgs(in)
	for i := range got {
		if got[i] != in[i] {
			t.Errorf("flag %d mangled: got %q want %q", i, got[i], in[i])
		}
	}
}

func TestScrubArgs_PathInsideArg(t *testing.T) {
	// Some args concatenate paths with other text (e.g. concat lists).
	in := []string{"concat:/Users/mika/a.wav|/Users/mika/b.wav"}
	got := ScrubArgs(in)
	if strings.Contains(got[0], "mika") {
		t.Errorf("username leaked: %q", got[0])
	}
}

func TestScrubArgs_ExtensionlessPath(t *testing.T) {
	in := []string{"-i", "/Users/mika/somefolder"}
	got := ScrubArgs(in)
	if got[1] != "<path>" {
		t.Errorf("extensionless path: got %q", got[1])
	}
}

func TestScrubOutput_PathInStderr(t *testing.T) {
	in := `Input #0, wav, from '/Users/mika/Music/secret.wav':
  Duration: 00:03:42.18, bitrate: 1411 kb/s`
	got := ScrubOutput(in)
	if strings.Contains(got, "mika") || strings.Contains(got, "secret") {
		t.Errorf("path leaked: %q", got)
	}
	if !strings.Contains(got, "Duration: 00:03:42.18") {
		t.Errorf("duration line lost: %q", got)
	}
}

func TestScrubOutput_MetadataBlockDropped(t *testing.T) {
	in := `Input #0, mp3, from '<path>':
  Metadata:
    title           : Secret Song
    artist          : Real Artist
    album           : Confidential
    encoder         : LAME3.99r
  Duration: 00:03:42.18`
	got := ScrubOutput(in)
	for _, leak := range []string{"Secret", "Real Artist", "Confidential", "LAME"} {
		if strings.Contains(got, leak) {
			t.Errorf("metadata leaked %q in output: %q", leak, got)
		}
	}
	if !strings.Contains(got, "Duration") {
		t.Errorf("non-metadata content stripped: %q", got)
	}
}

func TestScrubOutput_OutputToPath(t *testing.T) {
	in := `Output #0, flac, to '/Users/mika/exports/song.flac':`
	got := ScrubOutput(in)
	if strings.Contains(got, "mika") || strings.Contains(got, "song.flac") {
		t.Errorf("output path leaked: %q", got)
	}
}

func TestScrubOutput_LoudnormJSONPreserved(t *testing.T) {
	// Loudnorm measurement output is the *signal* we want.
	in := `[Parsed_loudnorm_0 @ 0x600003a4c000]
{
	"input_i" : "-19.45",
	"input_tp" : "-3.21",
	"input_lra" : "8.30"
}`
	got := ScrubOutput(in)
	if !strings.Contains(got, "input_i") || !strings.Contains(got, "-19.45") {
		t.Errorf("loudnorm output mangled: %q", got)
	}
}

func TestScrubOutput_Empty(t *testing.T) {
	if ScrubOutput("") != "" {
		t.Errorf("empty input should pass through")
	}
}

func TestTailBytes(t *testing.T) {
	long := strings.Repeat("x", 5000)
	got := TailBytes(long, 100)
	if !strings.HasPrefix(got, "…") {
		t.Errorf("expected ellipsis prefix, got %q", got[:10])
	}
	if len(got) != len("…")+100 {
		t.Errorf("unexpected length %d", len(got))
	}

	short := "abc"
	if TailBytes(short, 100) != short {
		t.Errorf("short string should pass through")
	}
}
