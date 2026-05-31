package audio

import (
	"strings"
	"testing"
)

// FilterChain is the accumulator that replaced the per-stage temp-file
// writes. The invariants it must preserve, drawn one-to-one from the old
// temp-file behavior:
//
//   1. With no stages, the chain is empty (no -af) — input passes through
//      to whatever the final encoder does.
//   2. The first non-empty stage runs at source rate, and an
//      aresample=192000 hop is inserted immediately after it. Later
//      stages therefore operate at 192 kHz, matching the upsample the
//      second temp file performed in the old pipeline.
//   3. Empty filters never trigger the upsample (so absent stages do not
//      silently change the resampling boundary).
//   4. The upsample is inserted exactly once across the chain's lifetime.
//   5. The chain reads as a valid ffmpeg filtergraph (comma-joined, no
//      leading/trailing/double commas).

func TestFilterChainEmpty(t *testing.T) {
	var c FilterChain
	if !c.IsEmpty() {
		t.Error("new chain should be empty")
	}
	if got := c.String(); got != "" {
		t.Errorf("empty chain String() = %q, want \"\"", got)
	}
	if got := c.Prefix("astats"); got != "astats" {
		t.Errorf("empty chain Prefix(\"astats\") = %q, want \"astats\"", got)
	}
	if got := c.Prefix(""); got != "" {
		t.Errorf("empty chain Prefix(\"\") = %q, want \"\"", got)
	}
}

func TestFilterChainSingleAddInjectsUpsample(t *testing.T) {
	var c FilterChain
	c.Add("highpass=f=70")

	want := "highpass=f=70,aresample=192000"
	if got := c.String(); got != want {
		t.Errorf("single Add String() = %q, want %q", got, want)
	}
	if c.IsEmpty() {
		t.Error("chain with one Add should not be empty")
	}
}

func TestFilterChainUpsampleInsertedOnlyOnce(t *testing.T) {
	var c FilterChain
	c.Add("highpass=f=70")
	c.Add("dynaudnorm=p=0.95")
	c.Add("acompressor=threshold=0.5")

	got := c.String()
	want := "highpass=f=70,aresample=192000,dynaudnorm=p=0.95,acompressor=threshold=0.5"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	if count := strings.Count(got, "aresample=192000"); count != 1 {
		t.Errorf("aresample=192000 appears %d times, want exactly 1", count)
	}
}

func TestFilterChainEmptyAddsAreNoops(t *testing.T) {
	// Mirrors a real-world pipeline where, e.g., buildDynaudnormFilter
	// returns "" because dynParams were unusable, or hot-peak attenuation
	// doesn't trigger because the input isn't hot. Such stages must not
	// pollute the chain — and crucially must not consume the
	// "first non-empty Add triggers upsample" slot.
	var c FilterChain
	c.Add("")
	c.Add("")

	if !c.IsEmpty() {
		t.Errorf("after only-empty Adds, IsEmpty() = false, want true")
	}
	if got := c.String(); got != "" {
		t.Errorf("after only-empty Adds, String() = %q, want \"\"", got)
	}

	c.Add("eq=...")
	c.Add("")
	c.Add("compressor")

	got := c.String()
	want := "eq=...,aresample=192000,compressor"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if count := strings.Count(got, "aresample=192000"); count != 1 {
		t.Errorf("upsample count = %d, want 1", count)
	}
}

func TestFilterChainNoLeadingTrailingOrDoubleCommas(t *testing.T) {
	// ffmpeg rejects ",,filter" or "filter,,". Make sure the chain never
	// produces them, no matter the Add pattern.
	patterns := [][]string{
		{"a"},
		{"a", "b"},
		{"a", "b", "c", "d"},
		{"", "a", "", "b", ""},
		{"", "", "a"},
		{"a", "", "", "b"},
	}
	for _, adds := range patterns {
		var c FilterChain
		for _, f := range adds {
			c.Add(f)
		}
		got := c.String()
		if strings.HasPrefix(got, ",") || strings.HasSuffix(got, ",") || strings.Contains(got, ",,") {
			t.Errorf("Add pattern %v produced malformed chain %q", adds, got)
		}
	}
}

func TestFilterChainPrefixComposesWithAnalysisFilter(t *testing.T) {
	// Prefix is how each analysis pass measures "the signal that earlier
	// stages would produce" without rendering it: it runs
	// `-af "<chain>,<analysisFilter>" -f null -`. The composition must
	// produce the same string callers would build by hand.
	var c FilterChain
	c.Add("highpass=f=70")
	c.Add("dynaudnorm")

	got := c.Prefix("astats=metadata=1")
	want := "highpass=f=70,aresample=192000,dynaudnorm,astats=metadata=1"
	if got != want {
		t.Errorf("Prefix() = %q, want %q", got, want)
	}

	if got := c.Prefix(""); got != c.String() {
		t.Errorf("Prefix(\"\") = %q, want chain String() = %q", got, c.String())
	}
}

func TestFilterChainCoversAllStageCombinations(t *testing.T) {
	// Exhaustively walk the {EQ, DynNorm, Compression} on/off matrix and
	// pin down the chain string each one produces. If any of these change,
	// the pipeline's measure→process cascade has shifted and the test
	// will catch it.
	const eq = "eq"
	const dyn = "dyn"
	const comp = "comp"

	tests := []struct {
		name       string
		eqOn       bool
		dynNormOn  bool
		compOn     bool
		wantChain  string
		wantPrefix string // chain composed with a trailing loudnorm probe
	}{
		{"all off", false, false, false, "", "loudnorm"},
		{"eq only", true, false, false, "eq,aresample=192000", "eq,aresample=192000,loudnorm"},
		{"dyn only", false, true, false, "dyn,aresample=192000", "dyn,aresample=192000,loudnorm"},
		{"comp only", false, false, true, "comp,aresample=192000", "comp,aresample=192000,loudnorm"},
		{"eq+dyn", true, true, false, "eq,aresample=192000,dyn", "eq,aresample=192000,dyn,loudnorm"},
		{"eq+comp", true, false, true, "eq,aresample=192000,comp", "eq,aresample=192000,comp,loudnorm"},
		{"dyn+comp", false, true, true, "dyn,aresample=192000,comp", "dyn,aresample=192000,comp,loudnorm"},
		{"all on", true, true, true, "eq,aresample=192000,dyn,comp", "eq,aresample=192000,dyn,comp,loudnorm"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var c FilterChain
			if tc.eqOn {
				c.Add(eq)
			}
			if tc.dynNormOn {
				c.Add(dyn)
			}
			if tc.compOn {
				c.Add(comp)
			}

			if got := c.String(); got != tc.wantChain {
				t.Errorf("chain = %q, want %q", got, tc.wantChain)
			}
			if got := c.Prefix("loudnorm"); got != tc.wantPrefix {
				t.Errorf("Prefix(\"loudnorm\") = %q, want %q", got, tc.wantPrefix)
			}

			// Across all eight cases, the upsample is either absent (when
			// no stage ran) or present exactly once.
			expectUpsamples := 0
			if tc.eqOn || tc.dynNormOn || tc.compOn {
				expectUpsamples = 1
			}
			if got := strings.Count(c.String(), "aresample=192000"); got != expectUpsamples {
				t.Errorf("upsample count = %d, want %d", got, expectUpsamples)
			}
		})
	}
}

func TestFilterChainPrefixIndependentOfSuffix(t *testing.T) {
	// Calling Prefix must not mutate the chain — the same chain may be
	// probed multiple times (astats, loudnorm, ebur128, …) and the chain
	// itself must remain untouched for the final render.
	var c FilterChain
	c.Add("eq")
	c.Add("dyn")

	before := c.String()
	_ = c.Prefix("astats")
	_ = c.Prefix("loudnorm")
	_ = c.Prefix("ebur128")
	after := c.String()

	if before != after {
		t.Errorf("Prefix mutated chain: before=%q after=%q", before, after)
	}
}
