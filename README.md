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