# TNT Plus
TNT Plus Transcodes, Normalizes, Tags, and Processes your audio files.
## Usage
Find pre-compiled programs from [releases](https://github.com/fremen-fi/TNT/releases) or from the [product page](https://fremen.fi/software/tnt).
### Two modes
#### Fast mode
Select from three presets for common broadcast scenarios. Processing requires four clicks. Files process individually in the background with completed files appearing in your output folder.

See [fast mode in manuals](https://www.fremen.fi/tnt-manual/fast-mode) for more information.
#### Advanced mode
Configure encoding parameters: format, sample rate, bit depth, and bitrate. Set custom loudness normalization targets or write ReplayGain tags.
Normalization alters audio data. Tagging writes metadata only. Choose one.

See [advanced mode in manuals](https://www.fremen.fi/tnt-manual/advanced-mode) for more information.

### Video input
TNT also accepts video files (MP4, MOV, MKV, and others). When a video file is queued, choose whether to drop the video and process the audio only, or keep the video and remux the processed audio back into the original container. In CLI mode, use the `-video-action` flag.

### Encoders
TNT ships with five encoders (six for macOS). The encoders are:

- FLAC
- MP3 (libmp3lame)
- Opus (libopus)
- AAC
	- Apple AAC (for macOS only)
	- Fraunhofer FDK-AAC (for all platforms)
- WAV

For more information about the encoders, see the [manual entry](https://www.fremen.fi/tnt-manual/encoders).

## Processing
Configure dynamics processing and EQ to shape your audio for different broadcast scenarios. TNT uses a [proprietary equation](https://www.fremen.fi/guru/dynamic-score) to find the best processing values for each audio file. For more information, see [processing in the manual](https://www.fremen.fi/tnt-manual/processing).

### Dynamics processing and FFmpeg
Loudness and dynamics (compression, upward compression, lookahead/conformity limiting, LRA reduction) run in an **in-house, time-domain DSP engine written in Go** — they are not FFmpeg filters. FFmpeg is used only to decode the source to raw PCM, encode the processed result, resample, and apply Shibata dithering on 16-bit output. Keeping the DSP native lets the encoder pre-limiter be calibrated per encoder (e.g. Apple AAC's overshoot at a given bitrate) and keeps processing behavior fully within TNT's control rather than an external filter graph's.

An optional FFmpeg `dynaudnorm` pass (`-dyn-norm`) is still available alongside the native engine for cases that want frame-based dynamic normalization instead.

## CLI Mode

TNT can run as a headless daemon that watches a directory and processes audio files automatically. Launch it with flags instead of the GUI.

### Basic usage

```bash
tnt -i /path/to/watch -o /path/to/output [options]
```

When started, TNT processes any existing audio files in the input directory, then watches for new files. Press `Ctrl+C` to stop.

### Examples

Normalize to EBU R128 with flat EQ and moderate dynamics:
```bash
tnt -i ./inbox -o ./processed -p:eq 1 -p:dyn 2 -lufs 1 -lufs-target-i -23 -lufs-target-tp -1
```

Tag files with ReplayGain without normalizing (FLAC output):
```bash
tnt -i ./inbox -o ./tagged -rg 1 -format flac
```

MP3 320kbps with broadcast dynamics and LUFS normalization:
```bash
tnt -i ./inbox -o ./out -format mp3 -br 320 -p:dyn 3 -lufs 1
```

Opus speech-optimized output with custom loudness target:
```bash
tnt -i ./inbox -o ./out -format opus -br 128 -speech 1 -lufs 1 -lufs-target-i -16 -lufs-target-tp -1
```

### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `-i` | Input directory to watch **(required)** | — |
| `-o` | Output directory **(required)** | — |
| `-format` | Output format: `pcm`, `flac`, `opus`, `aac`, `mp3` | `pcm` |
| `-sr` | Sample rate: `44100`, `48000`, `88200`, `96000`, `192000` | `48000` |
| `-bd` | Bit depth: `16`, `24`, `32`, `64` (32/64 are float) | `24` |
| `-br` | Bitrate in kbps (lossy codecs) | `256` |
| `-p:eq` | EQ preset: `0`=off, `1`=flat, `2`=speech, `3`=broadcast | `0`/off |
| `-p:dyn` | Dynamics preset: `0`=off, `1`=light, `2`=moderate, `3`=broadcast | `0`/off |
| `-lufs` | LUFS normalization: `1`=on, `0`=off | `1`/on |
| `-lufs-target-i` | Integrated loudness target in LUFS | `-23` |
| `-lufs-target-tp` | True peak limit in dBTP | `-1` |
| `-rg` | ReplayGain tag-only mode (no normalization): `1`=on, `0`=off | `0` |
| `-dyn-norm` | Dynamic normalization (dynaudnorm): `1`=on, `0`=off | `0` |
| `-speech` | Opus speech optimization: `1`=on, `0`=off | `0` |
| `-no-transcode` | Copy codec without transcoding: `1`=on, `0`=off | `0` |
| `-comp` | Data compression level `0`–`10` (FLAC/Opus only) | `0` |
| `-phase-check` | Check for phase inversion before processing: `1`=on, `0`=off | `0` |
| `-workers` | Number of parallel worker threads (`0`=auto: CPU cores − 1) | `0` |
| `-video-action` | Video input handling: `drop` (audio only) or `remux` (keep video, replace audio) | `drop` |
| `-ebu` | Use EBU R128 -defined values for loudness (-23 LUFS-I, -1 dBTP) | `0`/false |
| `-h`/`--help` | Print help and defaults | |
| `-v`/`--version` | Print version | |

#### About the `-comp`-flag
This flag is the amount of data compression you want for codecs that allow you to specify data compression level. It **does not** follow the encoder's own data compression argument, but maps to that argument.

Range is from 0 (no data compression) to 10 (as much data compression as the encoder allows).

### Notes

- The processing pipeline is identical to the GUI: EQ, dynamic normalization, dynamics/compression, then loudness normalization — all at 192 kHz / 64-bit float internally.
- When `-rg 1` is set, files are measured and tagged with `REPLAYGAIN_TRACK_GAIN`, `REPLAYGAIN_TRACK_PEAK`, and `REPLAYGAIN_REFERENCE_LOUDNESS` but audio data is not normalized.
- Logs are written to `~/.config/TNT/tnt-cli.log`.
- Running the binary without any flags launches the GUI as usual.

## Streaming module

[`normalizer/`](normalizer/) is a self-contained, stdlib-only Go module holding a **streaming** port of the loudness/dynamics engine — `io.Reader → io.Writer`, with bounded, O(lookahead) memory instead of holding the whole file resident. This is what lets TNT's processing run on **low-memory systems**, such as constrained server/cloud instances, where loading an entire multi-GiB PCM buffer into RAM isn't viable. It mirrors the math in `go/audio` (used by the desktop app, which needs random access for its editor/preview) rather than depending on it directly — see [`normalizer/README.md`](normalizer/README.md) for the two-implementations tradeoff and how they're kept in sync.

## License
[LICENSE.md](https://github.com/fremen-fi/TNT/blob/main/LICENSE.md), or view the most up-to-date [General License](https://www.fremen.fi/terms-of-use) on our website.
