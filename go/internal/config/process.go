package config

// ProcessConfig holds all processing options for a file
type ProcessConfig struct {
	Format         string
	SampleRate     string
	BitDepth       string
	Bitrate        string
	UseLoudnorm    bool
	CustomLoudnorm bool
	IsSpeech       bool
	WriteTags      bool
	NoTranscode    bool
	OriginIsAAC    bool
	DataCompLevel  int8
	DynamicsPreset string
	BypassProc     bool
	EqTarget       string
	DynNorm        bool
}
