package normalizer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"runtime"
	"testing"
)

// goldenHashes pin ApplyStage / Conform output bytes, recorded from main before
// the real-time API landed. Float rounding (FMA fusion) is arch-specific, so
// hashes are keyed by GOARCH; other architectures skip. Recorded from main.
var goldenHashes = map[string]struct{ stage, conform string }{
	"arm64": {
		stage:   "b98dd6a25354c31d65e2856de39a274c15df68aecd021e6fac2224fb54147bef",
		conform: "a08e96be7981df7258b528006245a012a1928aca4d33efe426f4f5002a16040f",
	},
	// Identical to arm64 at GOAMD64=v1 (no FMA fusion). GOAMD64=v3 is not covered.
	"amd64": {
		stage:   "b98dd6a25354c31d65e2856de39a274c15df68aecd021e6fac2224fb54147bef",
		conform: "a08e96be7981df7258b528006245a012a1928aca4d33efe426f4f5002a16040f",
	},
}

func goldenPCM() []byte {
	const sr = 44100
	l, r := testSignal(sr * 4)
	inter := make([]float32, len(l)*2)
	for i := range l {
		inter[i*2] = float32(l[i] * 0.5)
		inter[i*2+1] = float32(r[i] * 0.5)
	}
	var b bytes.Buffer
	writeF32LE(&b, inter)
	return b.Bytes()
}

func TestGoldenPodcorePaths(t *testing.T) {
	const sr = 44100
	pcm := goldenPCM()

	m, err := MeasureChain(bytes.NewReader(pcm), sr)
	if err != nil {
		t.Fatal(err)
	}
	sp := DeriveStage(m, 7)
	var stageOut bytes.Buffer
	if err := ApplyStage(bytes.NewReader(pcm), &stageOut, sr, sp); err != nil {
		t.Fatal(err)
	}
	var conformOut bytes.Buffer
	if err := Conform(bytes.NewReader(stageOut.Bytes()), &conformOut, sr, 6, -5, 20, 80); err != nil {
		t.Fatal(err)
	}
	// Chunk-boundary coverage: a second conform with an odd gain, shorter lookahead.
	var conform2 bytes.Buffer
	if err := Conform(bytes.NewReader(pcm), &conform2, sr, 12, -1, 5, 50); err != nil {
		t.Fatal(err)
	}

	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	gotStage := sum(stageOut.Bytes())
	gotConform := sum(append(conformOut.Bytes(), conform2.Bytes()...))
	t.Logf("GOARCH=%s stage=%s conform=%s", runtime.GOARCH, gotStage, gotConform)

	want, ok := goldenHashes[runtime.GOARCH]
	if !ok {
		t.Skipf("no golden hashes recorded for GOARCH=%s", runtime.GOARCH)
	}
	if want.stage == "" {
		t.Skip("golden hashes not recorded yet")
	}
	if gotStage != want.stage {
		t.Errorf("ApplyStage output changed: got %s want %s", gotStage, want.stage)
	}
	if gotConform != want.conform {
		t.Errorf("Conform output changed: got %s want %s", gotConform, want.conform)
	}
}
