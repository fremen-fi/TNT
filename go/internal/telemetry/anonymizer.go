// Package telemetry implements opt-in, anonymized usage telemetry for TNT.
//
// The anonymizer scrubs filesystem paths and audio metadata from ffmpeg
// argument lists and stderr output before any data leaves the device.
package telemetry

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Sentinels used in the scrubbed output. Kept short so they read well in logs.
const (
	pathSentinel = "<path>"
	fileSentinel = "<file>"
	redacted     = "<redacted>"
)

// Compiled once; safe for concurrent use.
var (
	// Path-like substrings inside arbitrary text. Order matters: longer/more
	// specific patterns first so they don't get partially eaten by shorter ones.
	rePathSubstring = regexp.MustCompile(
		`(?i)` +
			`[a-z]:\\users\\[^\\\s'"]+(?:\\[^\\\s'"]+)*` + // C:\Users\foo\bar
			`|/users/[^/\s'"]+(?:/[^/\s'"]+)*` + // /Users/foo/bar
			`|/home/[^/\s'"]+(?:/[^/\s'"]+)*` + // /home/foo/bar
			`|/var/folders/[^/\s'"]+(?:/[^/\s'"]+)*` + // macOS temp
			`|/private/var/[^/\s'"]+(?:/[^/\s'"]+)*` + // macOS sandboxed temp
			`|/tmp/[^/\s'"]+(?:/[^/\s'"]+)*`, // /tmp/...
	)

	// Lines from ffmpeg's metadata block — the indented "key : value" lines
	// that follow "Metadata:" headers. We drop them entirely.
	reMetadataLine = regexp.MustCompile(
		`(?i)^\s+(?:` +
			`title|artist|album|album_artist|composer|performer|` +
			`comment|description|copyright|date|year|genre|` +
			`track|disc|publisher|encoded_by|encoder|isrc|lyrics|` +
			`grouping|conductor|bpm|location|purl|synopsis|` +
			`creation_time|handler_name` +
			`)\s*:.*$`,
	)

	// Filenames inside ffmpeg "Input #N, fmt, from '...':" lines.
	reInputFrom = regexp.MustCompile(`(from\s+')[^']+(')`)

	// Filenames inside ffmpeg "Output #N, fmt, to '...':" lines.
	reOutputTo = regexp.MustCompile(`(to\s+')[^']+(')`)
)

// ScrubArgs returns a copy of args with paths and metadata values redacted.
//
// Rules:
//   - Any arg that looks like an absolute path is replaced with
//     `<path>/<file>.EXT` (extension preserved, basename and directory dropped).
//   - For `-metadata KEY=VALUE` pairs the value is replaced with `<redacted>`.
//     The KEY is kept so we can see which fields are being written.
//   - Other args pass through unchanged.
func ScrubArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		// Handle -metadata KEY=VALUE: we look one ahead so we redact the *next*
		// arg, not this one.
		if i > 0 && args[i-1] == "-metadata" {
			out[i] = scrubMetadataKV(a)
			continue
		}

		if isAbsPath(a) {
			out[i] = scrubPathArg(a)
			continue
		}

		// An arg may contain an embedded path (e.g. as part of a filter graph
		// that references an external file). Run the substring scrubber.
		out[i] = rePathSubstring.ReplaceAllString(a, pathSentinel)
	}
	return out
}

// ScrubOutput returns text with paths redacted and metadata-block lines
// dropped. Intended for ffmpeg stderr/stdout content.
func ScrubOutput(s string) string {
	if s == "" {
		return s
	}

	lines := strings.Split(s, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if reMetadataLine.MatchString(line) {
			continue
		}
		line = reInputFrom.ReplaceAllString(line, "${1}"+pathSentinel+"${2}")
		line = reOutputTo.ReplaceAllString(line, "${1}"+pathSentinel+"${2}")
		line = rePathSubstring.ReplaceAllString(line, pathSentinel)
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// TailBytes returns at most n bytes from the end of s, prefixed with an
// ellipsis if truncated. Useful for capping output_tail size.
func TailBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// --- helpers ---

func isAbsPath(s string) bool {
	if s == "" {
		return false
	}
	if filepath.IsAbs(s) {
		return true
	}
	// Windows drive-letter paths register as absolute via filepath.IsAbs only
	// when running on Windows. Detect them explicitly so cross-platform tests
	// behave consistently.
	if len(s) >= 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/') &&
		((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) {
		return true
	}
	return false
}

func scrubPathArg(p string) string {
	ext := filepath.Ext(p)
	if ext == "" {
		return pathSentinel
	}
	return pathSentinel + "/" + fileSentinel + ext
}

func scrubMetadataKV(kv string) string {
	eq := strings.IndexByte(kv, '=')
	if eq < 0 {
		return kv
	}
	return kv[:eq+1] + redacted
}
