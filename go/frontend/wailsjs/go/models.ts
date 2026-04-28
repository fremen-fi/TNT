export namespace main {
	
	export class Preferences {
	    advanced_mode: boolean;
	    last_output_dir: string;
	    simple_mode_selection: string;
	    format: string;
	    sample_rate: string;
	    bit_depth: string;
	    bitrate: string;
	    loudnorm_enabled: boolean;
	    custom_loudnorm: boolean;
	    normalize_target: string;
	    normalize_target_tp: string;
	    normalization_standard: string;
	    data_comp_level: number;
	    eq_preset: string;
	    dyn_preset: string;
	    dyn_norm_enabled: boolean;
	    selected_tab: string;
	    phase_check_auto: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Preferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.advanced_mode = source["advanced_mode"];
	        this.last_output_dir = source["last_output_dir"];
	        this.simple_mode_selection = source["simple_mode_selection"];
	        this.format = source["format"];
	        this.sample_rate = source["sample_rate"];
	        this.bit_depth = source["bit_depth"];
	        this.bitrate = source["bitrate"];
	        this.loudnorm_enabled = source["loudnorm_enabled"];
	        this.custom_loudnorm = source["custom_loudnorm"];
	        this.normalize_target = source["normalize_target"];
	        this.normalize_target_tp = source["normalize_target_tp"];
	        this.normalization_standard = source["normalization_standard"];
	        this.data_comp_level = source["data_comp_level"];
	        this.eq_preset = source["eq_preset"];
	        this.dyn_preset = source["dyn_preset"];
	        this.dyn_norm_enabled = source["dyn_norm_enabled"];
	        this.selected_tab = source["selected_tab"];
	        this.phase_check_auto = source["phase_check_auto"];
	    }
	}
	export class ProcessConfig {
	    Format: string;
	    SampleRate: string;
	    BitDepth: string;
	    Bitrate: string;
	    UseLoudnorm: boolean;
	    CustomLoudnorm: boolean;
	    IsSpeech: boolean;
	    WriteTags: boolean;
	    NoTranscode: boolean;
	    OriginIsAAC: boolean;
	    DataCompLevel: number;
	    DynamicsPreset: string;
	    BypassProc: boolean;
	    EqTarget: string;
	    DynNorm: boolean;
	    PhaseCheck: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProcessConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Format = source["Format"];
	        this.SampleRate = source["SampleRate"];
	        this.BitDepth = source["BitDepth"];
	        this.Bitrate = source["Bitrate"];
	        this.UseLoudnorm = source["UseLoudnorm"];
	        this.CustomLoudnorm = source["CustomLoudnorm"];
	        this.IsSpeech = source["IsSpeech"];
	        this.WriteTags = source["WriteTags"];
	        this.NoTranscode = source["NoTranscode"];
	        this.OriginIsAAC = source["OriginIsAAC"];
	        this.DataCompLevel = source["DataCompLevel"];
	        this.DynamicsPreset = source["DynamicsPreset"];
	        this.BypassProc = source["BypassProc"];
	        this.EqTarget = source["EqTarget"];
	        this.DynNorm = source["DynNorm"];
	        this.PhaseCheck = source["PhaseCheck"];
	    }
	}
	export class VersionInfo {
	    version: string;
	    os: string[];
	    download_url: any[];
	    release_notes: string;
	
	    static createFrom(source: any = {}) {
	        return new VersionInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.os = source["os"];
	        this.download_url = source["download_url"];
	        this.release_notes = source["release_notes"];
	    }
	}

}

