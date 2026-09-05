package main

import "strings"

// remuxAudioFamily normalizes an actualCodec ffmpeg encoder/codec name (as
// resolved from cfg.Format via platformCodecMap/config.GetCodec) into the
// coarse audio family used to check remux container compatibility.
func remuxAudioFamily(actualCodec string) string {
	switch actualCodec {
	case "libopus":
		return "opus"
	case "libfdk_aac", "aac", "aac_at":
		return "aac"
	case "libmp3lame":
		return "mp3"
	case "PCM":
		return "pcm"
	case "flac":
		return "flac"
	default:
		return ""
	}
}

// remuxContainerAudioFamilies lists, per video container extension
// (lowercase, with leading dot), which audio families ffmpeg's muxer will
// accept in a stream-copy ("-c copy") remux. Verified empirically against
// ffmpeg (n7.1.5) for every extension isVideoFile() recognizes:
//
//   - mp4/mkv/ts are permissive containers and accept all five.
//   - mov and the "ipod" muxer (.m4v) and 3gp are QuickTime/MPEG-4 family
//     but each has a narrower codec tag table than plain mp4.
//   - webm (matroska/webm muxer restricted to webm profile) only allows
//     Vorbis or Opus audio - none of our other codecs are legal there.
//   - mpg/mpeg (the "mpeg" program-stream muxer) only accepts mp1/mp2/mp3
//     (and pcm_dvd/pcm_s16be, which we don't produce).
//   - flv rejects FLAC outright and rejects Opus at the 48kHz rate our
//     encoder uses.
var remuxContainerAudioFamilies = map[string]map[string]bool{
	".mp4":  {"opus": true, "aac": true, "mp3": true, "pcm": true, "flac": true},
	".mkv":  {"opus": true, "aac": true, "mp3": true, "pcm": true, "flac": true},
	".ts":   {"opus": true, "aac": true, "mp3": true, "pcm": true, "flac": true},
	".mov":  {"aac": true, "mp3": true, "pcm": true},
	".webm": {"opus": true},
	".avi":  {"aac": true, "mp3": true, "pcm": true, "flac": true},
	".wmv":  {"aac": true, "mp3": true, "pcm": true, "flac": true},
	".flv":  {"aac": true, "mp3": true, "pcm": true},
	".m4v":  {"aac": true},
	".3gp":  {"aac": true},
	".mpg":  {"mp3": true},
	".mpeg": {"mp3": true},
}

// remuxCompatible reports whether actualCodec can be stream-copied into a
// container with the given file extension (e.g. ".mp4") when muxing the
// encoded audio back with the original video. An unrecognized codec or
// extension is allowed through rather than blocked, since the table isn't
// meant to be exhaustive beyond our own output formats and isVideoFile()'s
// extension list.
func remuxCompatible(actualCodec, containerExt string) bool {
	family := remuxAudioFamily(actualCodec)
	if family == "" {
		return true
	}
	allowed, ok := remuxContainerAudioFamilies[strings.ToLower(containerExt)]
	if !ok {
		return true
	}
	return allowed[family]
}
