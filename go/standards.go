package main

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Standard is a loudness normalization standard. Key is the wire identity: it
// is what the frontend sends and what is stored in preferences.json (see the
// norm-std radio values in frontend/index.html). UnmarshalJSON keys off it, so
// the Go keys and the frontend values must stay in sync — otherwise every
// standard silently degrades to Custom.
type Standard struct {
	Key               string
	TargetI, TargetTP float64
}

var (
	EBU    = Standard{"ebu", -23., -1.}
	ATSC   = Standard{"atsc", -24., -2.}
	AES    = Standard{"aes", -16., -1.}
	Custom = Standard{Key: "custom"}
)

func (s Standard) ITarget(isSpeech bool) float64 {
	if s == AES && isSpeech {
		return s.TargetI - 2
	}
	return s.TargetI
}

// parseTargetDB parses a user-entered dB target (e.g. "-16", "16", " -1 ").
// Loudness and true-peak targets are negative, so a positive entry is treated
// as negative. Empty or unparseable input returns fallback.
func parseTargetDB(s string, fallback float64) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fallback
	}
	if v > 0 {
		v = -v
	}
	return v
}

// resolveLoudnessTarget picks the integrated-loudness and true-peak targets (dB)
// for a run. The user's custom values (normTarget / normTargetTp) win when EITHER
// the standard radio is Custom OR the "custom loudness targets" toggle is on —
// the toggle is independent of the radio, so a user can enable custom values
// while the radio still shows a named standard. Empty custom fields fall back to
// the supplied defaults. Otherwise the named standard supplies the targets, with
// AES attenuating speech by 2 LU via ITarget.
func resolveLoudnessTarget(std Standard, customLoudnorm bool, normTarget, normTargetTp string, isSpeech bool, defTarget, defTargetTp float64) (target, targetTp float64) {
	if std == Custom || customLoudnorm {
		return parseTargetDB(normTarget, defTarget), parseTargetDB(normTargetTp, defTargetTp)
	}
	return std.ITarget(isSpeech), std.TargetTP
}

func (s Standard) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Key)
}

func (s *Standard) UnmarshalJSON(data []byte) error {
	var key string
	if err := json.Unmarshal(data, &key); err != nil {
		return err
	}
	switch key {
	case EBU.Key:
		*s = EBU
	case ATSC.Key:
		*s = ATSC
	case AES.Key:
		*s = AES
	default:
		*s = Custom
	}
	return nil
}
