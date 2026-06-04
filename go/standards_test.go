package main

import "testing"

// TestResolveLoudnessTarget locks the target-resolution logic, including the
// regression where the custom-loudness toggle is enabled while the standard
// radio still shows a named standard — the case that silently used the
// standard's defaults (e.g. AES -16/-1) instead of the user's entry.
func TestResolveLoudnessTarget(t *testing.T) {
	const defT, defTP = -23.0, -1.0

	tests := []struct {
		name           string
		std            Standard
		customLoudnorm bool
		normTarget     string
		normTargetTp   string
		isSpeech       bool
		wantTarget     float64
		wantTargetTp   float64
	}{
		{"EBU", EBU, false, "", "", false, -23, -1},
		{"ATSC", ATSC, false, "", "", false, -24, -2},
		{"AES music", AES, false, "", "", false, -16, -1},
		{"AES speech attenuates 2 LU", AES, false, "", "", true, -18, -1},
		{"Custom standard with values", Custom, false, "-6", "-2", false, -6, -2},
		{"Custom standard empty falls back", Custom, false, "", "", false, -23, -1},

		// The bug: toggle on, radio still on a named standard. Custom must win.
		{"toggle overrides named standard", AES, true, "-6", "-2", false, -6, -2},
		{"toggle with positive entry is negated", AES, true, "6", "2", false, -6, -2},
		{"toggle empty falls back to defaults", AES, true, "", "", false, -23, -1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotT, gotTP := resolveLoudnessTarget(
				tc.std, tc.customLoudnorm,
				tc.normTarget, tc.normTargetTp,
				tc.isSpeech, defT, defTP,
			)
			if gotT != tc.wantTarget || gotTP != tc.wantTargetTp {
				t.Fatalf("resolveLoudnessTarget = (%.1f, %.1f), want (%.1f, %.1f)",
					gotT, gotTP, tc.wantTarget, tc.wantTargetTp)
			}
		})
	}
}

// TestApplyConfigThreadsCustomTarget locks the plumbing half of the bug: the
// custom target values must travel from the Process request into the normalizer.
// Before the fix ProcessConfig had no such fields, so applyConfig could not copy
// them and the Advanced-view entry never reached the backend.
func TestApplyConfigThreadsCustomTarget(t *testing.T) {
	n := &AudioNormalizer{}
	n.applyConfig(ProcessConfig{
		CustomLoudnorm:    true,
		NormalizeTarget:   "-6",
		NormalizeTargetTp: "-2",
	})

	if n.normalizeTarget != "-6" {
		t.Errorf("normalizeTarget = %q, want %q", n.normalizeTarget, "-6")
	}
	if n.normalizeTargetTp != "-2" {
		t.Errorf("normalizeTargetTp = %q, want %q", n.normalizeTargetTp, "-2")
	}
	if !n.customLoudnorm {
		t.Error("customLoudnorm = false, want true")
	}
}
