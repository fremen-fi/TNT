// Command audition applies one of the time-domain dynamics processors to an
// audio file and writes a loudness-matched WAV you can listen to, printing the
// before/after LUFS / TP / LRA so the effect on loudness range is visible.
//
// Usage:
//
//	go run ./cmd/audition -in track.m4a -proc compress
//	go run ./cmd/audition -in track.m4a -proc compress -thresh -22 -ratio 4 -release 1500
//	go run ./cmd/audition -in track.m4a -proc character -thresh -3 -attack 5
//	go run ./cmd/audition -in track.m4a -proc conform   -thresh -1 -lookahead 5
//
// The output is loudness-matched to the input by default (-match), so an A/B is
// honest — you hear what the processor does to the dynamics, not just a level
// change. Pass -match=false to hear the raw, un-rematched result.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/fremen-fi/tnt/go/audio"
)

func main() {
	in := flag.String("in", "", "input audio file (any format ffmpeg can read)")
	out := flag.String("out", "", "output WAV (default: <in>.<proc>.wav)")
	proc := flag.String("proc", "compress", "processor: compress | character | conform")
	rate := flag.Int("rate", 48000, "work sample rate")
	ffmpegBin := flag.String("ffmpeg", "ffmpeg", "ffmpeg binary")
	match := flag.Bool("match", true, "loudness-match the output to -target LUFS")
	target := flag.Float64("target", -16, "loudness-match target, LUFS")
	targetLRA := flag.Float64("target-lra", 7, "target LRA for -proc lra")
	passes := flag.Int("passes", 4, "max LRA reduction passes for -proc lra")
	tp := flag.Float64("tp", -1, "final brick-wall ceiling (dBFS) appended to the lra chain")

	thresh := flag.Float64("thresh", -20, "threshold / ceiling, dBFS")
	knee := flag.Float64("knee", 6, "soft-knee width, dB (compress/character)")
	attack := flag.Float64("attack", 100, "attack, ms (compress/character)")
	release := flag.Float64("release", 1000, "release, ms (all)")
	ratio := flag.Float64("ratio", 4, "ratio (compress)")
	makeup := flag.Float64("makeup", 0, "makeup gain, dB (compress)")
	rms := flag.Float64("rms", 0, "RMS detector window, ms (compress; 0 = peak detection)")
	lookahead := flag.Float64("lookahead", 5, "lookahead, ms (conform)")

	flag.Parse()

	if *in == "" {
		fmt.Fprintln(os.Stderr, "audition: -in is required")
		flag.Usage()
		os.Exit(2)
	}

	outPath := *out
	if outPath == "" {
		outPath = *in + "." + *proc + ".wav"
	}

	// Work file: decode to a float WAV at the work rate. The processors read and
	// rewrite this in place; we re-encode to the listenable output at the end.
	work := outPath + ".work.wav"
	defer os.Remove(work)
	if err := ffmpegRun(*ffmpegBin, "-y", "-i", *in, "-ar", itoa(*rate), "-acodec", "pcm_f32le", work); err != nil {
		die("decode failed: %v", err)
	}

	var err error
	switch *proc {
	case "compress":
		err = audio.Compress(work, *thresh, *ratio, *knee, *attack, *release, *makeup, *rms)
	case "character":
		err = audio.CharacterLimiter(work, *thresh, *knee, *attack, *release)
	case "conform":
		err = audio.LookaheadLimiter(work, *thresh, *lookahead, *release)
	case "lra":
		var final float64
		final, err = audio.ReduceLRA(work, *targetLRA, *passes)
		if err == nil {
			fmt.Printf("  lra reduced to %.1f (target %.1f)\n", final, *targetLRA)
		}
	default:
		die("unknown -proc %q (want compress | character | conform | lra)", *proc)
	}
	if err != nil {
		die("processing failed: %v", err)
	}

	m, err := audio.MeasureLUFS(work)
	if err != nil {
		die("measure failed: %v", err)
	}
	if *match {
		if err := audio.Gain(work, *target-m.Integrated); err != nil {
			die("loudness match failed: %v", err)
		}
	}

	// Final brick-wall limiter — the last element of the real chain. It both stops
	// the match makeup from clipping AND pulls LRA down further by clamping the
	// loudest passages. Part of the lra chain only.
	if *proc == "lra" {
		// Very fast brick wall — trade a little artefact risk for tight conformance.
		if err := audio.LookaheadLimiter(work, *tp, 1, 30); err != nil {
			die("final limiter failed: %v", err)
		}
	}

	// Measure the complete chain.
	final, err := audio.MeasureLUFS(work)
	if err != nil {
		die("final measure failed: %v", err)
	}

	// Re-encode to 24-bit so it plays everywhere.
	if err := ffmpegRun(*ffmpegBin, "-y", "-i", work, "-acodec", "pcm_s24le", outPath); err != nil {
		die("encode failed: %v", err)
	}

	fmt.Printf("%-9s LRA %4.1f  LUFS %6.1f  TP %6.1f  ->  %s\n", *proc, final.LRA, final.Integrated, final.TruePeak, outPath)
}

func ffmpegRun(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "audition: "+format+"\n", args...)
	os.Exit(1)
}
