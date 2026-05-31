'use strict';

// TNT — Wails wiring. Talks to Go via window.go.main.AudioNormalizer.* and
// window.runtime.EventsOn. Pure UI helpers live in ui.js.

/**
 * @typedef {Object} Prefs Wire shape of Go `Preferences` over Wails JSON
 *   (snake_case, matching the json tags on the Go struct).
 * @property {boolean} advanced_mode
 * @property {string}  last_output_dir
 * @property {string}  simple_mode_selection
 * @property {string}  format
 * @property {string}  sample_rate
 * @property {string}  bit_depth
 * @property {string}  bitrate
 * @property {boolean} loudnorm_enabled
 * @property {boolean} custom_loudnorm
 * @property {string}  normalize_target
 * @property {string}  normalize_target_tp
 * @property {string}  normalization_standard
 * @property {number}  data_comp_level
 * @property {string}  eq_preset
 * @property {string}  dyn_preset
 * @property {boolean} dyn_norm_enabled
 * @property {string}  selected_tab
 * @property {boolean} phase_check_auto
 * @property {boolean} telemetry_enabled
 */

/**
 * @typedef {Object} ProcessConfig Mirror of Go `ProcessConfig` struct in main.go.
 * @property {string}  Format
 * @property {string}  SampleRate
 * @property {string}  BitDepth
 * @property {string}  Bitrate
 * @property {boolean} UseLoudnorm
 * @property {boolean} CustomLoudnorm
 * @property {boolean} IsSpeech
 * @property {boolean} WriteTags
 * @property {boolean} NoTranscode
 * @property {boolean} OriginIsAAC
 * @property {number}  DataCompLevel
 * @property {string}  DynamicsPreset
 * @property {boolean} BypassProc
 * @property {string}  EqTarget
 * @property {boolean} DynNorm
 * @property {boolean} PhaseCheck
 */

/** @typedef {Record<string, string>} MetadataTags */
/** @typedef {'ok' | 'info' | 'err'} LogType */

/**
 * @typedef {Object} AppState
 * @property {string[]} files
 * @property {number|null} selectedIdx
 * @property {boolean} processing
 * @property {boolean} watching
 * @property {string} normalizationStandard
 * @property {string[]} [metadataFields]
 */

/** @type {AppState} */
const state = {
    files: [],
    selectedIdx: null,
    processing: false,
    watching: false,
    normalizationStandard: 'EBU R128 (-23 LUFS)',
};

// Fast-tab radios use short keys; map to backend names. The advanced
// dropdown is populated from GetPlatformFormats() and stores backend
// names directly, so it doesn't need this map.
/** @type {Record<string, string>} */
const FAST_FORMAT_MAP = {
    pcm: 'PCM',
    flac: 'FLAC',
    aac: 'AAC',
    mp3: 'MPEG-II L3',
    opus: 'Opus',
};

/** @type {Record<string, string>} */
const FORMAT_LABELS = {
    'PCM': 'PCM (WAV)',
    'FLAC': 'FLAC',
    'AAC': 'AAC',
    'AAC (Fraunhofer)': 'AAC — Fraunhofer FDK',
    'AAC (Apple)': 'AAC — Apple AudioToolbox',
    'MPEG-II L3': 'MP3',
    'Opus': 'Opus',
};

/** @returns {Promise<void>} */
async function populateFormatDropdown() {
    const sel = $select('adv-format');
    if (!sel) return;
    /** @type {string[]} */
    let formats = [];
    try {
        formats = await window.go.main.AudioNormalizer.GetPlatformFormats();
    } catch (_) {
        formats = ['Opus', 'AAC', 'MPEG-II L3', 'PCM', 'FLAC'];
    }
    sel.innerHTML = '';
    formats.forEach((name) => {
        const opt = document.createElement('option');
        opt.value = name;
        opt.textContent = FORMAT_LABELS[name] || name;
        sel.appendChild(opt);
    });
    // Default to PCM when present.
    if (Array.from(sel.options).some((o) => o.value === 'PCM')) sel.value = 'PCM';
}

/** @type {Record<string, string>} */
const NORM_STD_MAP = {
    ebu: 'EBU R128 (-23 LUFS)',
    atsc: 'USA ATSC A/85 (-24 LUFS)',
    aes77: 'AES77-2023 (-16/-18 LUFS)',
    custom: 'Custom',
};
/** @type {Record<string, string>} */
const NORM_STD_REVERSE = {
    'EBU R128 (-23 LUFS)': 'ebu',
    'USA ATSC A/85 (-24 LUFS)': 'atsc',
    'AES77-2023 (-16/-18 LUFS)': 'aes77',
    'Custom': 'custom',
};

/** @type {Record<string, string>} */
const META_MAP = {
    'mt-title': 'title',
    'mt-artist': 'artist',
    'mt-album': 'album',
    'mt-year': 'date',
    'mt-comment': 'comment',
    'mt-track': 'track',
};

/* ── Helpers ── */
/** @param {string} path @returns {string} */
function basename(path) {
    if (!path) return '';
    const parts = String(path).split(/[\\/]/);
    return parts[parts.length - 1];
}
/** @param {string} path @returns {string} */
function ext(path) {
    const m = /\.([^./\\]+)$/.exec(String(path));
    return m ? m[1].toUpperCase() : '';
}
/** @param {string} s @returns {string} */
function parseSR(s) { return (s || '').replace(/\D/g, ''); }
/** @param {string} s @returns {string} */
function parseBD(s) {
    const t = (s || '').toLowerCase();
    if (t.startsWith('16')) return '16';
    if (t.startsWith('24')) return '24';
    if (t.startsWith('32')) return '32 (float)';
    if (t.startsWith('64')) return '64 (float)';
    return '24';
}

/* ── File list rendering ── */
/** @param {string[]} paths */
function renderFileList(paths) {
    state.files = Array.isArray(paths) ? paths : [];
    const list = $('file-list');
    if (!list) return;
    list.innerHTML = '';

    if (!state.files.length) {
        state.selectedIdx = null;
        const empty = document.createElement('div');
        empty.className = 'file-empty';
        empty.textContent = 'Drop audio files here\nor use the buttons above';
        list.appendChild(empty);
    } else {
        if (state.selectedIdx == null || state.selectedIdx >= state.files.length) {
            state.selectedIdx = 0;
        }
        state.files.forEach((path, i) => {
            const item = document.createElement('div');
            item.className = 'file-item' + (i === state.selectedIdx ? ' selected' : '');

            const badge = document.createElement('div');
            badge.className = 'file-badge';
            const badgeText = document.createElement('span');
            badgeText.textContent = ext(path);
            badge.appendChild(badgeText);

            const info = document.createElement('div');
            info.className = 'file-info';
            const nameEl = document.createElement('div');
            nameEl.className = 'file-name';
            nameEl.textContent = basename(path);
            const metaEl = document.createElement('div');
            metaEl.className = 'file-meta';
            metaEl.textContent = path;
            metaEl.title = path;
            info.appendChild(nameEl);
            info.appendChild(metaEl);

            const remove = document.createElement('button');
            remove.className = 'file-remove';
            remove.title = 'Remove';
            const removeIcon = document.createElement('span');
            removeIcon.className = 'nrk-icon icon-close';
            remove.appendChild(removeIcon);

            item.appendChild(badge);
            item.appendChild(info);
            item.appendChild(remove);

            item.addEventListener('click', () => {
                state.selectedIdx = i;
                renderFileList(state.files);
            });
            remove.addEventListener('click', async (e) => {
                e.stopPropagation();
                try {
                    const updated = await window.go.main.AudioNormalizer.RemoveFile(i);
                    if (state.selectedIdx === i) state.selectedIdx = null;
                    else if (state.selectedIdx != null && state.selectedIdx > i) state.selectedIdx -= 1;
                    renderFileList(updated);
                } catch (err) {
                    addLog('Remove failed: ' + err, 'err');
                }
            });

            list.appendChild(item);
        });
    }

    const has = state.files.length > 0;
    const proc = $button('btn-process');
    const clr = $button('btn-clear');
    if (proc) proc.disabled = !has || state.processing;
    if (clr) clr.disabled = !has;
    updateMetaTab();
}

function updateMetaTab() {
    const path = (state.selectedIdx != null) ? state.files[state.selectedIdx] : null;
    const hint = $('meta-hint');
    if (hint) {
        hint.textContent = path ?
            `Editing: ${basename(path)}` :
            'Select a single file in the queue to edit its metadata.';
    }
    const has = !!path;
    const grid = $('meta-grid');
    if (grid) {
        grid.classList.toggle('disabled', !has);
        grid.querySelectorAll('input').forEach((i) => {
            // Read-only fields (track total, RG values) stay non-editable but
            // still enabled when a file is selected so they show their value.
            if (i.hasAttribute('readonly')) { i.disabled = !has; return; }
            i.disabled = !has;
        });
    }
    // Clear the RG rows when no file is selected.
    if (!has) setRGRow('', '');
    const r = $button('meta-read');
    const w = $button('meta-write');
    if (r) r.disabled = !has;
    if (w) w.disabled = !has;
}

/* ── Inline-handler entry points ── */
/** @param {'files'|'folder'} kind */
async function addMockFile(kind) {
    try {
        const files = kind === 'folder' ?
            await window.go.main.AudioNormalizer.SelectFolder() :
            await window.go.main.AudioNormalizer.SelectFiles();
        renderFileList(files || []);
    } catch (err) {
        addLog('Select failed: ' + err, 'err');
    }
}

async function clearFiles() {
    try {
        await window.go.main.AudioNormalizer.ClearFiles();
        state.selectedIdx = null;
        renderFileList([]);
    } catch (err) {
        addLog('Clear failed: ' + err, 'err');
    }
}

async function setOutput() {
    try {
        const dir = await window.go.main.AudioNormalizer.SetOutputFolder();
        if (dir) {
            const out = $('output-path');
            if (out) {
                out.textContent = dir;
                out.title = dir;
            }
            addLog('Output folder set', 'info');
        }
    } catch (err) {
        addLog('Output folder failed: ' + err, 'err');
    }
}

async function handleProcess() {
    if (!state.files.length || state.processing) return;
    state.processing = true;
    const procBtn = $button('btn-process');
    if (procBtn) procBtn.disabled = true;
    setProgress(0);
    try {
        await window.go.main.AudioNormalizer.Process(buildConfig());
    } catch (err) {
        addLog('Process failed: ' + err, 'err');
        state.processing = false;
        clearProgress();
        if (procBtn) procBtn.disabled = state.files.length === 0;
    }
}

async function previewSize() {
    try {
        await window.go.main.AudioNormalizer.PreviewSize(buildConfig());
    } catch (err) {
        addLog('Preview failed: ' + err, 'err');
    }
}

/* ── ProcessConfig builder from current DOM ── */
/** @returns {ProcessConfig} */
function buildConfig() {
    const activeBtn = /** @type {HTMLElement|null} */ (document.querySelector('.tab-btn.active'));
    const activeTab = (activeBtn && activeBtn.dataset.tab) || 'fast';

    const dyn = $select('proc-dyn');
    const eq = $select('proc-eq');
    const dn = $input('proc-dn');
    const byp = $input('proc-bypass');
    const phs = $input('prefs-phase');

    const proc = {
        DynamicsPreset: dyn ? dyn.value : 'Off',
        EqTarget: eq ? eq.value : 'Off',
        DynNorm: dn ? dn.checked : false,
        BypassProc: byp ? byp.checked : false,
        PhaseCheck: phs ? phs.checked : false,
    };

    if (activeTab === 'fast') {
        const presetEl = /** @type {HTMLInputElement|null} */ (document.querySelector('input[name="fast-preset"]:checked'));
        const preset = presetEl ? presetEl.value : 'pcm';
        const fastNorm = $input('fast-norm');
        const cfg = {
            ...proc,
            Format: '',
            SampleRate: '',
            BitDepth: '',
            Bitrate: '',
            UseLoudnorm: fastNorm ? fastNorm.checked : false,
            CustomLoudnorm: false,
            IsSpeech: false,
            WriteTags: false,
            NoTranscode: false,
            OriginIsAAC: false,
            DataCompLevel: 0,
        };
        switch (preset) {
            case 'aac':
                cfg.Format = FAST_FORMAT_MAP.aac;
                cfg.Bitrate = '256';
                break;
            case 'mp3':
                cfg.Format = FAST_FORMAT_MAP.mp3;
                cfg.Bitrate = '320';
                break;
            case 'pcm':
            default:
                cfg.Format = FAST_FORMAT_MAP.pcm;
                cfg.SampleRate = '48000';
                cfg.BitDepth = '24';
                break;
        }
        return cfg;
    }

    const fmt = $select('adv-format');
    const sr = $select('adv-sr');
    const bd = $select('adv-bd');
    const br = $input('adv-br');
    const norm = $input('adv-norm');
    const clud = $input('adv-custom-loud');
    const sp = $input('adv-speech');
    const rg = $input('adv-rg');
    const noTr = $input('adv-no-transcode');
    const cl = $input('adv-cl');

    return {
        ...proc,
        Format: fmt ? fmt.value : 'PCM',
        SampleRate: parseSR(sr ? sr.value : ''),
        BitDepth: parseBD(bd ? bd.value : ''),
        Bitrate: br ? br.value : '',
        UseLoudnorm: norm ? norm.checked : false,
        CustomLoudnorm: clud ? clud.checked : false,
        IsSpeech: sp ? sp.checked : false,
        WriteTags: rg ? rg.checked : false,
        NoTranscode: noTr ? noTr.checked : false,
        OriginIsAAC: false,
        DataCompLevel: parseInt(cl ? cl.value : '0', 10),
    };
}

/* ── Metadata read/write ── */
/** @param {string} gain @param {string} target */
function setRGRow(gain, target) {
    const rows = document.querySelectorAll('.rg-row');
    const showGain = !!gain;
    const showTarget = !!target;
    rows.forEach((el) => {
        // Each .rg-row is either the <label> (with htmlFor) or the <input> itself.
        const label = /** @type {HTMLLabelElement} */ (el);
        const isGain = el.id === 'mt-rg-gain' || label.htmlFor === 'mt-rg-gain';
        const isTarget = el.id === 'mt-rg-target' || label.htmlFor === 'mt-rg-target';
        if (isGain) el.classList.toggle('hidden', !showGain);
        if (isTarget) el.classList.toggle('hidden', !showTarget);
    });
    const g = $input('mt-rg-gain');
    if (g) g.value = gain || '';
    const t = $input('mt-rg-target');
    if (t) t.value = target || '';
}

/** @param {MetadataTags} tags @param {string[]} keys @returns {string} */
function pickFirst(tags, keys) {
    for (const k of keys) {
        const v = tags && tags[k];
        if (v && String(v).trim() !== '') return String(v);
    }
    return '';
}

async function readMetadataIntoForm() {
    if (state.selectedIdx == null) return;
    const path = state.files[state.selectedIdx];
    try {
        const tags = await window.go.main.AudioNormalizer.ReadMetadata(path);
        Object.entries(META_MAP).forEach(([id, key]) => {
            if (id === 'mt-track') return; // handled below
            const el = $input(id);
            if (el) el.value = (tags && tags[key]) || '';
        });

        // Track may be "5" or "5/12"; show total separately, read-only.
        const trackRaw = (tags && tags.track) || '';
        const [cur, total] = String(trackRaw).split('/').map((s) => s.trim());
        const trackEl = $input('mt-track');
        if (trackEl) trackEl.value = cur || '';
        const totalEl = $input('mt-track-total');
        if (totalEl) totalEl.value = total || '';
        const sep = $('mt-track-sep');
        if (sep) sep.style.visibility = total ? 'visible' : 'hidden';
        if (totalEl) totalEl.style.visibility = total ? 'visible' : 'hidden';

        // ReplayGain — read-only display, hidden if absent.
        const gain = pickFirst(tags, ['replaygain_track_gain', 'replaygain_album_gain']);
        const target = pickFirst(tags, ['replaygain_reference_loudness']);
        setRGRow(gain, target);

        addLog('Tags read from ' + basename(path), 'ok');
    } catch (err) {
        addLog('Read tags failed: ' + err, 'err');
    }
}

async function writeMetadataFromForm() {
    if (state.selectedIdx == null) return;
    const path = state.files[state.selectedIdx];
    /** @type {MetadataTags} */
    const tags = {};
    Object.entries(META_MAP).forEach(([id, key]) => {
        if (id === 'mt-track') return;
        const el = $input(id);
        if (el) tags[key] = el.value;
    });
    // Recombine track as "X/N" if total is present.
    const trackEl = $input('mt-track');
    const totalEl = $input('mt-track-total');
    const cur = trackEl ? trackEl.value.trim() : '';
    const total = totalEl ? totalEl.value.trim() : '';
    tags.track = total ? `${cur}/${total}` : cur;
    try {
        await window.go.main.AudioNormalizer.WriteMetadata(path, tags);
        addLog('Tags written to ' + basename(path), 'ok');
    } catch (err) {
        addLog('Write tags failed: ' + err, 'err');
    }
}

/* ── Preferences sync ── */
/** @param {Prefs | null | undefined} prefs */
function applyPrefsToUI(prefs) {
    if (!prefs) return;
    state.normalizationStandard = prefs.normalization_standard || 'EBU R128 (-23 LUFS)';

    if (prefs.simple_mode_selection) {
        const r = /** @type {HTMLInputElement|null} */ (document.querySelector(`input[name="fast-preset"][value="${prefs.simple_mode_selection}"]`));
        if (r) r.checked = true;
    }
    if (prefs.format) {
        const sel = $select('adv-format');
        if (sel && Array.from(sel.options).some((o) => o.value === prefs.format)) {
            sel.value = prefs.format;
        }
    }
    if (prefs.sample_rate) {
        const sel = $select('adv-sr');
        if (sel) {
            const want = parseSR(prefs.sample_rate);
            Array.from(sel.options).forEach((o) => { if (parseSR(o.textContent || '') === want) sel.value = o.value || (o.textContent || ''); });
        }
    }
    if (prefs.bit_depth) {
        const sel = $select('adv-bd');
        if (sel) {
            Array.from(sel.options).forEach((o) => { if (parseBD(o.textContent || '') === parseBD(prefs.bit_depth)) sel.value = o.value || (o.textContent || ''); });
        }
    }
    if (prefs.bitrate) { const el = $input('adv-br'); if (el) el.value = prefs.bitrate; }
    if (prefs.normalize_target) { const el = $input('adv-lufs'); if (el) el.value = prefs.normalize_target; }
    if (prefs.normalize_target_tp) { const el = $input('adv-tp'); if (el) el.value = prefs.normalize_target_tp; }
    if (typeof prefs.loudnorm_enabled === 'boolean') {
        const fn = $input('fast-norm');
        if (fn) fn.checked = prefs.loudnorm_enabled;
        const an = $input('adv-norm');
        if (an) an.checked = prefs.loudnorm_enabled;
    }
    if (typeof prefs.custom_loudnorm === 'boolean') {
        const cl = $input('adv-custom-loud');
        if (cl) cl.checked = prefs.custom_loudnorm;
    }
    if (typeof prefs.data_comp_level === 'number') {
        const cl = $input('adv-cl');
        if (cl) cl.value = String(prefs.data_comp_level);
        const out = /** @type {HTMLOutputElement|null} */ (document.getElementById('adv-cl-out'));
        if (out) out.value = String(prefs.data_comp_level);
    }
    if (prefs.eq_preset) { const el = $select('proc-eq'); if (el) el.value = prefs.eq_preset; }
    if (prefs.dyn_preset) { const el = $select('proc-dyn'); if (el) el.value = prefs.dyn_preset; }
    if (typeof prefs.dyn_norm_enabled === 'boolean') { const dn = $input('proc-dn'); if (dn) dn.checked = prefs.dyn_norm_enabled; }
    if (typeof prefs.phase_check_auto === 'boolean') { const ph = $input('prefs-phase'); if (ph) ph.checked = prefs.phase_check_auto; }
    if (typeof prefs.telemetry_enabled === 'boolean') { const t = $input('prefs-telemetry-enabled'); if (t) t.checked = prefs.telemetry_enabled; }
    if (prefs.last_output_dir) {
        const out = $('output-path');
        if (out) {
            out.textContent = prefs.last_output_dir;
            out.title = prefs.last_output_dir;
        }
    }

    const stdKey = NORM_STD_REVERSE[state.normalizationStandard];
    if (stdKey) {
        const r = /** @type {HTMLInputElement|null} */ (document.querySelector(`input[name="norm-std"][value="${stdKey}"]`));
        if (r) r.checked = true;
    }
    const prefsLufs = $input('prefs-lufs');
    if (prefsLufs) prefsLufs.value = prefs.normalize_target || '-23';
    const prefsTp = $input('prefs-tp');
    if (prefsTp) prefsTp.value = prefs.normalize_target_tp || '-1';

    if (prefs.selected_tab) switchTab(prefs.selected_tab);
    updateAdvanced();
    updateNormSubText();
    const byp = $input('proc-bypass');
    updateBypass(byp ? byp.checked : false);
}

/** @returns {Prefs} */
function gatherPrefs() {
    const stdEl = /** @type {HTMLInputElement|null} */ (document.querySelector('input[name="norm-std"]:checked'));
    const stdKey = stdEl ? stdEl.value : 'ebu';
    const std = NORM_STD_MAP[stdKey] || state.normalizationStandard;
    const simpleEl = /** @type {HTMLInputElement|null} */ (document.querySelector('input[name="fast-preset"]:checked'));
    const simple = simpleEl ? simpleEl.value : '';
    const tabEl = /** @type {HTMLElement|null} */ (document.querySelector('.tab-btn.active'));
    const tab = (tabEl && tabEl.dataset.tab) || 'fast';

    const out = $('output-path');
    const fmt = $select('adv-format');
    const sr = $select('adv-sr');
    const bd = $select('adv-bd');
    const br = $input('adv-br');
    const an = $input('adv-norm');
    const clu = $input('adv-custom-loud');
    const cl = $input('adv-cl');
    const eq = $select('proc-eq');
    const dyn = $select('proc-dyn');
    const dn = $input('proc-dn');
    const ph = $input('prefs-phase');
    const advLufs = $input('adv-lufs');
    const advTp = $input('adv-tp');
    const prefsLufs = $input('prefs-lufs');
    const prefsTp = $input('prefs-tp');

    return {
        advanced_mode: tab === 'advanced' || tab === 'processing',
        last_output_dir: (out && out.title) || '',
        simple_mode_selection: simple,
        format: fmt ? fmt.value : '',
        sample_rate: parseSR(sr ? sr.value : ''),
        bit_depth: parseBD(bd ? bd.value : ''),
        bitrate: br ? br.value : '',
        loudnorm_enabled: an ? an.checked : false,
        custom_loudnorm: clu ? clu.checked : false,
        normalize_target: stdKey === 'custom' ? (prefsLufs ? prefsLufs.value : '-23') : (advLufs ? advLufs.value : '-23'),
        normalize_target_tp: stdKey === 'custom' ? (prefsTp ? prefsTp.value : '-1') : (advTp ? advTp.value : '-1'),
        normalization_standard: std,
        data_comp_level: parseInt(cl ? cl.value : '0', 10),
        eq_preset: eq ? eq.value : 'Off',
        dyn_preset: dyn ? dyn.value : 'Off',
        dyn_norm_enabled: dn ? dn.checked : false,
        selected_tab: tab,
        phase_check_auto: ph ? ph.checked : false,
        telemetry_enabled: (function() { const t = $input('prefs-telemetry-enabled'); return t ? t.checked : false; })(),
    };
}

/** @param {boolean} enabled */
async function setTelemetryEnabled(enabled) {
    try {
        await window.go.main.AudioNormalizer.SetTelemetryEnabled(!!enabled);
        addLog(enabled ? 'Anonymous telemetry enabled' : 'Anonymous telemetry disabled', 'info');
    } catch (e) {
        addLog('Failed to update telemetry preference', 'err');
    }
}

async function resetTelemetryID() {
    try {
        await window.go.main.AudioNormalizer.ResetTelemetryID();
        addLog('Anonymous telemetry ID reset', 'ok');
    } catch (e) {
        addLog('Failed to reset telemetry ID', 'err');
    }
}
window.setTelemetryEnabled = setTelemetryEnabled;
window.resetTelemetryID = resetTelemetryID;

/* ── Override inline mock handlers and bind extra listeners ── */
function bindRealHandlers() {
    // Metadata buttons — replace inline addLog stubs
    const r = $button('meta-read');
    if (r) {
        r.onclick = null;
        r.addEventListener('click', readMetadataIntoForm);
    }
    const w = $button('meta-write');
    if (w) {
        w.onclick = null;
        w.addEventListener('click', writeMetadataFromForm);
    }

    // Preferences pane buttons
    const prefsSavePane = $('prefs-save');
    if (prefsSavePane) {
        const [saveBtn, resetBtn] = prefsSavePane.querySelectorAll('button');
        if (saveBtn) {
            saveBtn.onclick = null;
            saveBtn.addEventListener('click', async () => {
                try {
                    await window.go.main.AudioNormalizer.SavePreferences(gatherPrefs());
                    addLog('Configuration saved', 'ok');
                } catch (err) { addLog('Save failed: ' + err, 'err'); }
            });
        }
        if (resetBtn) {
            resetBtn.onclick = null;
            resetBtn.addEventListener('click', async () => {
                try {
                    await window.go.main.AudioNormalizer.ResetPreferences();
                    const prefs = await window.go.main.AudioNormalizer.LoadPreferences();
                    applyPrefsToUI(prefs);
                    addLog('Settings reset to defaults', 'info');
                } catch (err) { addLog('Reset failed: ' + err, 'err'); }
            });
        }
    }

    const versionPane = $('prefs-version');
    if (versionPane) {
        const checkBtn = versionPane.querySelector('button');
        if (checkBtn) {
            checkBtn.onclick = null;
            checkBtn.addEventListener('click', async () => {
                try {
                    const v = await window.go.main.AudioNormalizer.CheckForUpdates();
                    if (v && /** @type {any} */ (v).version) addLog('Update available: ' + /** @type {any} */ (v).version, 'info');
                    else addLog('You are up to date', 'ok');
                } catch (err) { addLog('Update check failed: ' + err, 'err'); }
            });
        }
    }

    const reportPane = $('prefs-report');
    if (reportPane) {
        const sendBtn = reportPane.querySelector('button');
        if (sendBtn) {
            sendBtn.onclick = null;
            sendBtn.addEventListener('click', async () => {
                try {
                    await window.go.main.AudioNormalizer.SendLogReport();
                    addLog('Error report sent', 'info');
                } catch (err) { addLog('Send report failed: ' + err, 'err'); }
            });
        }
    }

    // Watch mode toggle
    const watch = $input('prefs-watch-mode');
    if (watch) {
        watch.addEventListener('change', async (e) => {
            const t = /** @type {HTMLInputElement} */ (e.target);
            try {
                if (t.checked) {
                    await window.go.main.AudioNormalizer.StartWatching();
                    state.watching = true;
                    const warn = $('watcher-warn');
                    if (warn) warn.textContent = 'WATCHING';
                    addLog('Watch mode enabled', 'info');
                } else {
                    await window.go.main.AudioNormalizer.StopWatching();
                    state.watching = false;
                    const warn = $('watcher-warn');
                    if (warn) warn.textContent = '';
                    addLog('Watch mode disabled', 'info');
                }
            } catch (err) {
                addLog('Watch toggle failed: ' + err, 'err');
                t.checked = state.watching;
            }
        });
    }

    // Norm-std radios → push into adv-lufs/adv-tp + keep state in sync
    document.querySelectorAll('input[name="norm-std"]').forEach((rEl) => {
        const r = /** @type {HTMLInputElement} */ (rEl);
        r.addEventListener('change', () => {
            const key = r.value;
            state.normalizationStandard = NORM_STD_MAP[key] || state.normalizationStandard;
            if (key === 'ebu') { setLT('-23', '-1'); }
            if (key === 'atsc') { setLT('-24', '-2'); }
            if (key === 'aes77') { setLT('-16', '-1'); }
            // 'custom' uses whatever is in prefs-lufs/prefs-tp
            updateNormSubText();
            updateAdvanced();
        });
    });

    // Live-update the Fast/Advanced normalize sub-labels while the user
    // edits the Preferences custom targets, or the per-session Advanced
    // custom targets, or toggles "Custom loudness targets" in Advanced.
    ['prefs-lufs', 'prefs-tp', 'adv-lufs', 'adv-tp'].forEach((id) => {
        const el = $input(id);
        if (el) el.addEventListener('input', updateNormSubText);
    });
    const advCustom = $input('adv-custom-loud');
    if (advCustom) advCustom.addEventListener('change', updateNormSubText);

    // Speech does not force Opus
    const sp = $input('adv-speech');
    if (sp) sp.addEventListener('change', (e) => {
        const t = /** @type {HTMLInputElement} */ (e.target);
        if (t.checked) {
            console.log('set speech to true')
        }
    });

    // Custom loudness onchange already fires updateAdvanced via inline; nothing extra.

    // Bypass already handled by ui.js updateBypass; nothing extra.
}

/** @param {string} lufs @param {string} tp */
function setLT(lufs, tp) {
    const a = $input('adv-lufs');
    if (a) a.value = lufs;
    const b = $input('adv-tp');
    if (b) b.value = tp;
    const c = $input('prefs-lufs');
    if (c) c.value = lufs;
    const d = $input('prefs-tp');
    if (d) d.value = tp;
    updateNormSubText();
}

/** Render the Normalize sub-labels in Fast and Advanced.
 *  Fast always reflects the Preferences-defined standard.
 *  Advanced's "Custom loudness targets" toggle, when on, overrides
 *  with the per-session adv-lufs / adv-tp values. */
function updateNormSubText() {
    const stdEl = /** @type {HTMLInputElement|null} */ (document.querySelector('input[name="norm-std"]:checked'));
    const stdKey = stdEl ? stdEl.value : 'ebu';
    let label, lufs, tp;
    if (stdKey === 'ebu') {
        label = 'EBU R128';
        lufs = '-23';
        tp = '-1';
    } else if (stdKey === 'atsc') {
        label = 'USA ATSC A/85';
        lufs = '-24';
        tp = '-2';
    } else if (stdKey === 'aes77') {
        label = 'AES77-2023';
        lufs = "-16";
        tp = '-1';
    } else {
        label = 'Custom';
        const l = $input('prefs-lufs');
        const t = $input('prefs-tp');
        lufs = l && l.value ? l.value : '-23';
        tp = t && t.value ? t.value : '-1';
    }
    /** @param {string} s */
    const fmt = (s) => String(s).replace(/^-/, '−');

    const fast = $('fast-norm-sub');
    if (fast) fast.textContent = `${label} · ${fmt(lufs)} LUFS · ${fmt(tp)} dBTP`;

    let advLabel = label,
        advLufs = lufs,
        advTp = tp;
    const advCustom = $input('adv-custom-loud');
    if (advCustom && advCustom.checked) {
        const al = $input('adv-lufs');
        const at = $input('adv-tp');
        advLabel = 'Custom';
        advLufs = al && al.value ? al.value : lufs;
        advTp = at && at.value ? at.value : tp;
    }
    const adv = $('adv-norm-sub');
    if (adv) adv.textContent = `${advLabel} · ${fmt(advLufs)} LUFS · ${fmt(advTp)} dBTP — alters audio`;
}

/* ── Wails runtime events ── */
function setupRuntimeEvents() {
    if (!window.runtime || !window.runtime.EventsOn) return;
    window.runtime.EventsOn('status:log', (msg) => addLog(String(msg), 'info'));
    window.runtime.EventsOn('progress:update', (val) => setProgress(Number(val)));
    window.runtime.EventsOn('progress:done', () => {
        state.processing = false;
        clearProgress();
        const procBtn = $button('btn-process');
        if (procBtn) procBtn.disabled = state.files.length === 0;
        addLog('Processing complete', 'ok');
    });
    window.runtime.EventsOn('file:added', (files) => renderFileList(files || []));
    window.runtime.EventsOn('update:available', (info) => {
        addLog('Update available: ' + (info && info.version ? info.version : 'see preferences'), 'info');
    });
    window.runtime.EventsOn('watch:file', (path) => addLog('Watch: ' + basename(String(path)), 'info'));
}

/* ── Init ── */
async function init() {
    setupRuntimeEvents();
    bindRealHandlers();

    await populateFormatDropdown();
    updateAdvanced();

    try {
        state.metadataFields = await window.go.main.AudioNormalizer.MetadataFields();
    } catch (_) { /* ignore */ }

    try {
        const version = await window.go.main.AudioNormalizer.GetVersion();
        const vEl = $('prefs-version-text');
        if (vEl) vEl.textContent = 'You are running version ' + version + '.';
    } catch (_) { /* ignore */ }

    try {
        const goos = await window.go.main.AudioNormalizer.GetOS();
        const rEl = $('prefs-report-text');
        if (rEl) {
            rEl.textContent = goos === 'windows' ?
                'Send an error report. The processing log will be copied to your desktop. You can then send it to us with your request to appsupport@collinsgroup.fi.' :
                'Send an error report. Opens your default email client with the processing log attached. If a mailing app cannot be found, the log will be copied to your desktop. You can then send that to us with your request to appsupport@collinsgroup.fi.';
        }
    } catch (_) { /* ignore */ }

    try {
        const prefs = await window.go.main.AudioNormalizer.LoadPreferences();
        applyPrefsToUI(prefs);
    } catch (err) {
        addLog('Failed to load preferences: ' + err, 'err');
    }

    try {
        const files = await window.go.main.AudioNormalizer.GetFiles();
        renderFileList(files || []);
    } catch (_) {
        renderFileList([]);
    }

    try {
        const out = await window.go.main.AudioNormalizer.GetOutputFolder();
        if (out) {
            const el = $('output-path');
            if (el) {
                el.textContent = out;
                el.title = out;
            }
        }
    } catch (_) { /* ignore */ }
}

if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
} else {
    init();
}

// Dev hot-reload: poll a token file written by dev.sh on every frontend change.
// In production builds the file isn't shipped, fetch returns 404, polling no-ops.
(function devReload() {
    /** @type {string|null} */
    let last = null;
    setInterval(async () => {
        try {
            const r = await fetch('./reload-token.txt?_=' + Date.now(), { cache: 'no-store' });
            if (!r.ok) return;
            const txt = (await r.text()).trim();
            if (last !== null && txt !== last) { location.reload(); return; }
            last = txt;
        } catch (_) { /* offline / 404 */ }
    }, 500);
})();