package main

import (
	"encoding/json"
	"os"
	"reflect"
	"runtime"
	"testing"
)

func TestIsAudioFile(t *testing.T) {
	want := map[string]bool{
		"track.mp3":          true,
		"PIECE.WAV":          true,
		"interview.aif":      true,
		"x.flac":             true,
		"a.opus":             true,
		"a.aiff":             true,
		"path/to/file.m4a":   true,
		"path\\to\\file.aac": true,
		"notes.txt":          false,
		"image.png":          false,
		"":                   false,
		"noext":              false,
	}
	for path, w := range want {
		if got := isAudioFile(path); got != w {
			t.Errorf("isAudioFile(%q) = %v, want %v", path, got, w)
		}
	}
}

func TestPlatformCodecMap(t *testing.T) {
	if platformCodecMap["AAC"] != "libfdk_aac" {
		t.Errorf("AAC mapping: %q", platformCodecMap["AAC"])
	}
	if runtime.GOOS == "darwin" {
		if platformCodecMap["AAC (Apple)"] != "aac_at" {
			t.Errorf("Apple AAC mapping missing/wrong: %q", platformCodecMap["AAC (Apple)"])
		}
	}
}

func TestGetPlatformFormatsNonEmpty(t *testing.T) {
	n := &AudioNormalizer{}
	got := n.GetPlatformFormats()
	if len(got) == 0 {
		t.Fatal("GetPlatformFormats() returned empty list")
	}
	have := map[string]bool{}
	for _, v := range got {
		have[v] = true
	}
	for _, must := range []string{"PCM", "FLAC", "Opus", "MPEG-II L3"} {
		if !have[must] {
			t.Errorf("formats missing %q (have %v)", must, got)
		}
	}
	if runtime.GOOS == "darwin" && !have["AAC (Apple)"] {
		t.Errorf("darwin formats should include AAC (Apple), got %v", got)
	}
}

func TestMetadataFieldsShape(t *testing.T) {
	n := &AudioNormalizer{}
	got := n.MetadataFields()
	required := []string{"title", "artist", "album", "date", "track", "comment"}
	have := map[string]bool{}
	for _, f := range got {
		have[f] = true
	}
	for _, k := range required {
		if !have[k] {
			t.Errorf("MetadataFields missing %q (have %v)", k, got)
		}
	}
}

func TestAddFileDeduplicates(t *testing.T) {
	n := &AudioNormalizer{}
	n.addFile("/tmp/a.wav")
	n.addFile("/tmp/b.wav")
	n.addFile("/tmp/a.wav") // duplicate
	got := n.GetFiles()
	if !reflect.DeepEqual(got, []string{"/tmp/a.wav", "/tmp/b.wav"}) {
		t.Errorf("dedup failed: %v", got)
	}
}

func TestApplyConfig(t *testing.T) {
	n := &AudioNormalizer{}
	cfg := ProcessConfig{
		Format: "FLAC", SampleRate: "48000", BitDepth: "24", Bitrate: "256",
		UseLoudnorm: true, CustomLoudnorm: false, IsSpeech: false,
		WriteTags: true, NoTranscode: false, OriginIsAAC: false,
		DataCompLevel: 5, DynamicsPreset: "Light", BypassProc: false,
		EqTarget: "Speech", DynNorm: true, PhaseCheck: true,
	}
	n.applyConfig(cfg)
	if n.format != "FLAC" || n.sampleRate != "48000" || n.bitDepth != "24" || n.bitrate != "256" {
		t.Errorf("format fields not applied: %+v", n)
	}
	if !n.useLoudnorm || !n.writeTags || !n.dynNorm || !n.phaseCheck {
		t.Error("bool flags not applied")
	}
	if n.dataCompLevel != 5 {
		t.Errorf("DataCompLevel not applied: %d", n.dataCompLevel)
	}
	if n.dynamicsPreset != "Light" || n.eqPreset != "Speech" {
		t.Errorf("processing presets not applied: dyn=%q eq=%q", n.dynamicsPreset, n.eqPreset)
	}
}

func TestProcessConfigJSONRoundTrip(t *testing.T) {
	in := ProcessConfig{
		Format:      "AAC",
		SampleRate:  "44100",
		BitDepth:    "16",
		Bitrate:     "256",
		UseLoudnorm: true,
		IsSpeech:    true,
		WriteTags:   false,
		DataCompLevel: 3,
		DynamicsPreset: "Moderate",
		BypassProc: true,
		EqTarget: "Broadcast",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out ProcessConfig
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch:\nin:  %+v\nout: %+v", in, out)
	}
}

func TestPreferencesJSONRoundTripUsesSnakeKeys(t *testing.T) {
	p := Preferences{
		AdvancedMode:          true,
		LastOutputDir:         "/tmp/out",
		Format:                "FLAC",
		SampleRate:            "48000",
		BitDepth:              "24",
		Bitrate:               "256",
		LoudnormEnabled:       true,
		CustomLoudnorm:        false,
		NormalizeTarget:       "-23",
		NormalizeTargetTp:     "-1",
		NormalizationStandard: "EBU R128 (-23 LUFS)",
		DataCompLevel:         5,
		EqPreset:              "Off",
		DynPreset:             "Light",
		DynNorm:               true,
		SelectedTab:           "advanced",
		PhaseCheck:            true,
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Must be wire-compatible with on-disk preferences (snake_case keys)
	for _, key := range []string{
		`"advanced_mode"`, `"last_output_dir"`, `"sample_rate"`, `"bit_depth"`,
		`"loudnorm_enabled"`, `"normalize_target"`, `"normalization_standard"`,
		`"data_comp_level"`, `"phase_check_auto"`, `"selected_tab"`,
	} {
		if !contains(string(b), key) {
			t.Errorf("missing JSON key %s in: %s", key, string(b))
		}
	}
	var out Preferences
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(p, out) {
		t.Errorf("round-trip mismatch:\nin:  %+v\nout: %+v", p, out)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestParseCLIFlagsHappyPath(t *testing.T) {
	saved := os.Args
	defer func() { os.Args = saved }()

	os.Args = []string{"tnt", "-i", "/in", "-o", "/out", "-format", "flac",
		"-sr", "44100", "-bd", "32", "-br", "192", "-lufs", "1",
		"-lufs-target-i", "23", "-lufs-target-tp", "1",
		"-rg", "0", "-p:eq", "2", "-p:dyn", "3",
		"-dyn-norm", "1", "-speech", "0", "-no-transcode", "0",
		"-comp", "8", "-phase-check", "1", "-workers", "4",
	}
	cfg, ok := parseCLIFlags()
	if !ok {
		t.Fatal("parseCLIFlags returned ok=false")
	}
	if cfg.Format != "FLAC" {
		t.Errorf("Format = %q want FLAC", cfg.Format)
	}
	if cfg.SampleRate != "44100" || cfg.Bitrate != "192" {
		t.Errorf("SR/BR = %q/%q", cfg.SampleRate, cfg.Bitrate)
	}
	if cfg.BitDepth != "32 (float)" {
		t.Errorf("BitDepth = %q want '32 (float)'", cfg.BitDepth)
	}
	if !cfg.LufsEnabled || cfg.RGOnly || !cfg.DynNorm || cfg.Speech || !cfg.PhaseCheck {
		t.Errorf("flags = %+v", cfg)
	}
	if cfg.EqPreset != 2 || cfg.DynPreset != 3 {
		t.Errorf("presets = eq:%d dyn:%d", cfg.EqPreset, cfg.DynPreset)
	}
	if cfg.LufsTargetI != "-23" || cfg.LufsTargetTP != "-1" {
		t.Errorf("LUFS targets must be negative: I=%q TP=%q", cfg.LufsTargetI, cfg.LufsTargetTP)
	}
	if cfg.Workers != 4 || cfg.DataComp != 8 {
		t.Errorf("misc = workers:%d comp:%d", cfg.Workers, cfg.DataComp)
	}
}

func TestParseCLIFlagsNoArgsReturnsFalse(t *testing.T) {
	saved := os.Args
	defer func() { os.Args = saved }()

	os.Args = []string{"tnt"}
	if _, ok := parseCLIFlags(); ok {
		t.Error("expected ok=false with no args")
	}
}

func TestParseCLIFlagsNonFlagFirstArgReturnsFalse(t *testing.T) {
	saved := os.Args
	defer func() { os.Args = saved }()

	os.Args = []string{"tnt", "filename.wav"}
	if _, ok := parseCLIFlags(); ok {
		t.Error("expected ok=false when first arg is not a flag")
	}
}
