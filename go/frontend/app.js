// TNT — frontend wiring. Talks to Go via window.go.main.AudioNormalizer.* and
// window.runtime.EventsOn / window.runtime.EventsEmit.

const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));

// ---------- DOM refs ----------
const els = {
    logo: () => $('#app-logo'),
    fileList: () => $('#file-list'),
    progress: () => $('#progress-bar'),
    statusLog: () => $('#status-log'),
    outputPath: () => $('#output-path'),
    watcherWarn: () => $('#watcher-warn'),
    btnProcess: () => $('#btn-process'),
    btnReadMeta: () => $('#btn-read-metadata'),
    btnWriteMeta: () => $('#btn-write-metadata'),
    metaStatus: () => $('#metadata-status'),
    metaForm: () => $('#metadata-form'),
    formatSelect: () => $('#format-select'),
};

const state = {
    files: [],
    metadataFields: [],
    normalizationStandard: 'EBU R128 (-23 LUFS)',
    watching: false,
};

// ---------- helpers ----------
function appendLog(msg) {
    const log = els.statusLog();
    if (!log) return;
    log.value += (log.value ? '\n' : '') + msg;
    log.scrollTop = log.scrollHeight;
}

function setProgress(fraction) {
    const p = els.progress();
    p.hidden = false;
    p.value = Math.max(0, Math.min(1, fraction));
}

function onProcessingDone() {
    const p = els.progress();
    p.hidden = true;
    p.value = 0;
}

function basename(path) {
    if (!path) return '';
    const m = path.split(/[\\/]/);
    return m[m.length - 1];
}

function renderFileList(files) {
    state.files = Array.isArray(files) ? files : [];
    const ul = els.fileList();
    ul.innerHTML = '';
    state.files.forEach((path, idx) => {
        const li = document.createElement('li');

        const name = document.createElement('span');
        name.className = 'file-name';
        name.textContent = basename(path);
        name.title = path;

        const remove = document.createElement('button');
        remove.className = 'remove';
        remove.textContent = '×';
        remove.title = 'Remove';
        remove.addEventListener('click', async () => {
            const updated = await window.go.main.AudioNormalizer.RemoveFile(idx);
            renderFileList(updated);
            updateProcessButtonState();
            updateMetadataButtons();
        });

        li.appendChild(name);
        li.appendChild(remove);
        ul.appendChild(li);
    });
    updateProcessButtonState();
    updateMetadataButtons();
}

function updateProcessButtonState() {
    els.btnProcess().disabled = state.files.length === 0;
}

function updateMetadataButtons() {
    const single = state.files.length === 1;
    els.btnReadMeta().disabled = !single;
    els.btnWriteMeta().disabled = !single;
    if (state.files.length === 0) {
        els.metaStatus().textContent = 'Select a single file to edit metadata';
    } else if (state.files.length === 1) {
        els.metaStatus().textContent = basename(state.files[0]);
    } else {
        els.metaStatus().textContent = `${state.files.length} files queued — select exactly one to edit metadata`;
    }
}

// ---------- format show/hide ----------
function onFormatChange() {
    const f = els.formatSelect().value || '';
    const isPCM = f === 'PCM';
    const isFLAC = f === 'FLAC';
    const isOpus = f.startsWith('Opus');

    setRowVisible('row-sample-rate', isPCM);
    setRowVisible('row-bit-depth', isPCM);
    setRowVisible('row-bitrate', !isPCM && !isFLAC);
    setRowVisible('row-comp-level', isFLAC || isOpus);
    setRowVisible('row-is-speech', isOpus);
    setRowVisible('row-no-transcode', !isPCM && !isFLAC);
}

function setRowVisible(id, visible) {
    const el = document.getElementById(id);
    if (!el) return;
    el.classList.toggle('hidden', !visible);
}

function setLufsTpVisible(visible) {
    setRowVisible('row-lufs', visible);
    setRowVisible('row-tp', visible);
}

// ---------- normalization label ----------
function refreshNormalizationLabel() {
    const std = state.normalizationStandard;
    const fastLabel = $('#fast-loudnorm-label');
    const advLabel = $('#adv-loudnorm-label');
    const writeTagsLabel = $('#write-tags-label');
    let label = 'Normalize (EBU R128: -23 LUFS)';
    let writeTagsText = 'Write RG tags (EBU R128: -23 LUFS)';
    if (std === 'USA ATSC A/85 (-24 LUFS)') {
        label = 'Normalize (ATSC A/85: -24 LUFS)';
        writeTagsText = 'Write RG tags (ATSC A/85: -24 LUFS)';
    } else if (std === 'Custom') {
        const lufs = $('#lufs-input').value || '-23';
        label = `Normalize (Custom: ${lufs} LUFS)`;
        writeTagsText = `Write RG tags (Custom: ${lufs} LUFS)`;
    }
    if (fastLabel) fastLabel.textContent = label;
    if (advLabel) advLabel.textContent = label;
    if (writeTagsLabel) writeTagsLabel.textContent = writeTagsText;
}

// ---------- ProcessConfig builder ----------
function getSimplePresetConfig() {
    const sel = document.querySelector('input[name="simple-preset"]:checked');
    const value = sel ? sel.value : 'Production (PCM 48kHz/24bit)';
    switch (value) {
        case 'Small file (AAC 256kbps)':
            return { Format: 'AAC', Bitrate: '256' };
        case 'Most compatible (MP3 320kbps)':
            return { Format: 'MPEG-II L3', Bitrate: '320' };
        case 'Production (PCM 48kHz/24bit)':
        default:
            return { Format: 'PCM', SampleRate: '48000', BitDepth: '24' };
    }
}

function buildConfig() {
    // Default empty config matching ProcessConfig fields the Go side expects.
    const cfg = {
        Format: '',
        SampleRate: '',
        BitDepth: '',
        Bitrate: '',
        UseLoudnorm: false,
        CustomLoudnorm: $('#custom-loudnorm').checked,
        IsSpeech: $('#is-speech').checked,
        WriteTags: $('#write-tags').checked,
        NoTranscode: $('#no-transcode').checked,
        OriginIsAAC: false,
        DataCompLevel: parseInt($('#comp-level-input').value || '0', 10),
        DynamicsPreset: $('#dynamics-select').value,
        BypassProc: $('#bypass-proc').checked,
        EqTarget: $('#eq-select').value,
        DynNorm: $('#dyn-norm').checked,
        PhaseCheck: false,
    };

    // Are we on the Fast tab?
    const activeTab = document.querySelector('.tab.active')?.dataset.tab;
    if (activeTab === 'fast') {
        Object.assign(cfg, getSimplePresetConfig());
        cfg.UseLoudnorm = $('#fast-loudnorm').checked;
    } else {
        cfg.Format = els.formatSelect().value;
        cfg.SampleRate = $('#sample-rate-select').value;
        cfg.BitDepth = $('#bit-depth-select').value;
        cfg.Bitrate = $('#bitrate-input').value || '0';
        cfg.UseLoudnorm = $('#adv-loudnorm').checked;
    }

    return cfg;
}

// ---------- tab switching ----------
function setupTabs() {
    $$('.tab').forEach((btn) => {
        btn.addEventListener('click', () => {
            $$('.tab').forEach((b) => b.classList.remove('active'));
            btn.classList.add('active');
            const target = btn.dataset.tab;
            $$('.tab-panel').forEach((p) => p.classList.remove('active'));
            const panel = document.getElementById(`tab-${target}`);
            if (panel) panel.classList.add('active');
        });
    });

    $$('.prefs-tab').forEach((btn) => {
        btn.addEventListener('click', () => {
            const group = btn.dataset.prefsTab ? '[data-prefs-tab]' : '[data-help-tab]';
            const dataKey = btn.dataset.prefsTab ? 'prefsTab' : 'helpTab';
            const panelPrefix = btn.dataset.prefsTab ? 'prefs-' : 'help-';
            const siblings = btn.parentElement.querySelectorAll(group);
            siblings.forEach((b) => b.classList.remove('active'));
            btn.classList.add('active');
            const dialog = btn.closest('dialog');
            dialog.querySelectorAll('.prefs-panel').forEach((p) => p.classList.remove('active'));
            const target = document.getElementById(`${panelPrefix}${btn.dataset[dataKey]}`);
            if (target) target.classList.add('active');
        });
    });
}

// ---------- metadata form ----------
function buildMetadataForm(fields) {
    const form = els.metaForm();
    form.innerHTML = '';
    fields.forEach((field) => {
        const label = document.createElement('label');
        const display = field.replace(/_/g, ' ').replace(/^./, (c) => c.toUpperCase());
        label.htmlFor = `meta-${field}`;
        label.textContent = display;

        const input = document.createElement('input');
        input.type = 'text';
        input.id = `meta-${field}`;
        input.name = field;
        input.placeholder = field;

        form.appendChild(label);
        form.appendChild(input);
    });
}

function readMetadataIntoForm(tags) {
    state.metadataFields.forEach((field) => {
        const input = document.getElementById(`meta-${field}`);
        if (input) input.value = (tags && tags[field]) || '';
    });
}

function metadataFromForm() {
    const tags = {};
    state.metadataFields.forEach((field) => {
        const input = document.getElementById(`meta-${field}`);
        tags[field] = input ? input.value : '';
    });
    return tags;
}

// ---------- preferences sync ----------
function applyPrefsToUI(prefs) {
    if (!prefs) return;
    state.normalizationStandard = prefs.NormalizationStandard || 'EBU R128 (-23 LUFS)';

    if (prefs.SimpleMode) {
        const r = document.querySelector(`input[name="simple-preset"][value="${cssEscape(prefs.SimpleMode)}"]`);
        if (r) r.checked = true;
    }
    if (prefs.Format) els.formatSelect().value = prefs.Format;
    if (prefs.SampleRate) $('#sample-rate-select').value = prefs.SampleRate;
    if (prefs.BitDepth) $('#bit-depth-select').value = prefs.BitDepth;
    if (prefs.Bitrate) $('#bitrate-input').value = prefs.Bitrate;
    if (prefs.NormalizeTarget) $('#lufs-input').value = prefs.NormalizeTarget;
    if (prefs.NormalizeTargetTp) $('#tp-input').value = prefs.NormalizeTargetTp;
    if (typeof prefs.LoudnormEnabled === 'boolean') {
        $('#fast-loudnorm').checked = prefs.LoudnormEnabled;
        $('#adv-loudnorm').checked = prefs.LoudnormEnabled;
    }
    if (typeof prefs.CustomLoudnorm === 'boolean') {
        $('#custom-loudnorm').checked = prefs.CustomLoudnorm;
        setLufsTpVisible(prefs.CustomLoudnorm);
    }
    if (typeof prefs.DataCompLevel === 'number') {
        $('#comp-level-input').value = prefs.DataCompLevel;
        $('#comp-level-output').value = prefs.DataCompLevel;
    }
    if (prefs.EqPreset) $('#eq-select').value = prefs.EqPreset;
    if (prefs.DynPreset) $('#dynamics-select').value = prefs.DynPreset;
    if (typeof prefs.DynNorm === 'boolean') $('#dyn-norm').checked = prefs.DynNorm;
    if (typeof prefs.PhaseCheck === 'boolean') $('#prefs-phase-check').checked = prefs.PhaseCheck;
    if (prefs.LastOutputDir) {
        els.outputPath().textContent = prefs.LastOutputDir;
        els.outputPath().title = prefs.LastOutputDir;
    }

    // Reflect in prefs dialog
    const stdRadio = document.querySelector(`input[name="norm-standard"][value="${cssEscape(state.normalizationStandard)}"]`);
    if (stdRadio) stdRadio.checked = true;
    $('#prefs-lufs').value = prefs.NormalizeTarget || '-23';
    $('#prefs-tp').value = prefs.NormalizeTargetTp || '-1';

    refreshNormalizationLabel();
    onFormatChange();
}

function gatherPrefs() {
    const std = document.querySelector('input[name="norm-standard"]:checked')?.value || state.normalizationStandard;
    const simple = document.querySelector('input[name="simple-preset"]:checked')?.value || '';
    return {
        AdvancedMode: false,
        LastOutputDir: els.outputPath().title || '',
        SimpleMode: simple,
        Format: els.formatSelect().value || '',
        SampleRate: $('#sample-rate-select').value,
        BitDepth: $('#bit-depth-select').value,
        Bitrate: $('#bitrate-input').value || '',
        LoudnormEnabled: $('#adv-loudnorm').checked,
        CustomLoudnorm: $('#custom-loudnorm').checked,
        NormalizeTarget: std === 'Custom' ? $('#prefs-lufs').value : ($('#lufs-input').value || '-23'),
        NormalizeTargetTp: std === 'Custom' ? $('#prefs-tp').value : ($('#tp-input').value || '-1'),
        NormalizationStandard: std,
        DataCompLevel: parseInt($('#comp-level-input').value || '0', 10),
        EqPreset: $('#eq-select').value,
        DynPreset: $('#dynamics-select').value,
        DynNorm: $('#dyn-norm').checked,
        SelectedTab: document.querySelector('.tab.active')?.dataset.tab || 'fast',
        PhaseCheck: $('#prefs-phase-check').checked,
    };
}

function cssEscape(s) {
    if (window.CSS && CSS.escape) return CSS.escape(s);
    return String(s).replace(/["\\]/g, '\\$&');
}

// ---------- logo theme ----------
function setupLogo() {
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const update = () => {
        const dark = mq.matches;
        els.logo().src = dark ? './assets/logo-dark.png' : './assets/logo-light.png';
    };
    if (mq.addEventListener) mq.addEventListener('change', update);
    else mq.addListener(update);
    update();
}

// ---------- wire buttons ----------
function setupButtons() {
    $('#btn-select-files').addEventListener('click', async () => {
        const files = await window.go.main.AudioNormalizer.SelectFiles();
        renderFileList(files);
    });

    $('#btn-select-folder').addEventListener('click', async () => {
        const files = await window.go.main.AudioNormalizer.SelectFolder();
        renderFileList(files);
    });

    $('#btn-output-folder').addEventListener('click', async () => {
        const dir = await window.go.main.AudioNormalizer.SetOutputFolder();
        if (dir) {
            els.outputPath().textContent = dir;
            els.outputPath().title = dir;
        }
    });

    $('#btn-clear').addEventListener('click', async () => {
        await window.go.main.AudioNormalizer.ClearFiles();
        renderFileList([]);
    });

    $('#btn-process').addEventListener('click', async () => {
        const cfg = buildConfig();
        await window.go.main.AudioNormalizer.Process(cfg);
    });

    $('#btn-preview-size').addEventListener('click', async () => {
        const cfg = buildConfig();
        await window.go.main.AudioNormalizer.PreviewSize(cfg);
    });

    $('#btn-help').addEventListener('click', () => $('#help-dialog').showModal());
    $('#btn-menu').addEventListener('click', () => $('#prefs-dialog').showModal());

    $('#btn-read-metadata').addEventListener('click', async () => {
        if (state.files.length !== 1) return;
        try {
            const tags = await window.go.main.AudioNormalizer.ReadMetadata(state.files[0]);
            readMetadataIntoForm(tags || {});
            els.metaStatus().textContent = `Read tags from ${basename(state.files[0])}`;
        } catch (err) {
            els.metaStatus().textContent = `Failed to read tags: ${err}`;
        }
    });

    $('#btn-write-metadata').addEventListener('click', async () => {
        if (state.files.length !== 1) return;
        try {
            const tags = metadataFromForm();
            await window.go.main.AudioNormalizer.WriteMetadata(state.files[0], tags);
            els.metaStatus().textContent = `Wrote tags to ${basename(state.files[0])}`;
        } catch (err) {
            els.metaStatus().textContent = `Failed to write tags: ${err}`;
        }
    });

    // Custom loudness toggles LUFS/TP rows
    $('#custom-loudnorm').addEventListener('change', (e) => {
        setLufsTpVisible(e.target.checked);
        if (e.target.checked) {
            state.normalizationStandard = 'Custom';
            const r = document.querySelector('input[name="norm-standard"][value="Custom"]');
            if (r) r.checked = true;
        }
        refreshNormalizationLabel();
    });

    // Format change
    els.formatSelect().addEventListener('change', onFormatChange);

    // Compression slider feedback
    $('#comp-level-input').addEventListener('input', (e) => {
        $('#comp-level-output').value = e.target.value;
    });

    // LUFS/TP input labels
    $('#lufs-input').addEventListener('input', refreshNormalizationLabel);

    // Bypass disables Dynamics + EQ visually
    $('#bypass-proc').addEventListener('change', (e) => {
        $('#dynamics-select').disabled = e.target.checked;
        $('#eq-select').disabled = e.target.checked;
    });

    // Speech forces Opus
    $('#is-speech').addEventListener('change', (e) => {
        if (e.target.checked) {
            const opts = Array.from(els.formatSelect().options).map((o) => o.value);
            const opus = opts.find((v) => v.startsWith('Opus'));
            if (opus) {
                els.formatSelect().value = opus;
                onFormatChange();
            }
        }
    });

    // Normalize / Write RG tags are mutually exclusive
    $('#adv-loudnorm').addEventListener('change', (e) => {
        if (e.target.checked) $('#write-tags').checked = false;
    });
    $('#write-tags').addEventListener('change', (e) => {
        if (e.target.checked) $('#adv-loudnorm').checked = false;
    });

    // Preferences dialog
    $$('input[name="norm-standard"]').forEach((r) => {
        r.addEventListener('change', () => {
            const std = r.value;
            state.normalizationStandard = std;
            switch (std) {
                case 'EBU R128 (-23 LUFS)':
                    $('#prefs-lufs').value = '-23';
                    $('#prefs-tp').value = '-1';
                    $('#lufs-input').value = '-23';
                    $('#tp-input').value = '-1';
                    break;
                case 'USA ATSC A/85 (-24 LUFS)':
                    $('#prefs-lufs').value = '-24';
                    $('#prefs-tp').value = '-2';
                    $('#lufs-input').value = '-24';
                    $('#tp-input').value = '-2';
                    break;
            }
            refreshNormalizationLabel();
        });
    });

    $('#prefs-save-btn').addEventListener('click', async () => {
        const prefs = gatherPrefs();
        await window.go.main.AudioNormalizer.SavePreferences(prefs);
        appendLog('Preferences saved.');
    });

    $('#prefs-reset-btn').addEventListener('click', async () => {
        await window.go.main.AudioNormalizer.ResetPreferences();
        appendLog('Preferences reset to defaults.');
    });

    $('#prefs-watch-mode').addEventListener('change', async (e) => {
        if (e.target.checked) {
            await window.go.main.AudioNormalizer.StartWatching();
            state.watching = true;
            els.watcherWarn().textContent = 'WATCHING';
        } else {
            await window.go.main.AudioNormalizer.StopWatching();
            state.watching = false;
            els.watcherWarn().textContent = '';
        }
    });

    $('#prefs-check-update').addEventListener('click', async () => {
        $('#prefs-update-result').textContent = 'Checking…';
        try {
            const v = await window.go.main.AudioNormalizer.CheckForUpdates();
            if (v && v.version) {
                $('#prefs-update-result').textContent = `Update available: ${v.version}`;
            } else {
                $('#prefs-update-result').textContent = 'You are up to date.';
            }
        } catch (err) {
            $('#prefs-update-result').textContent = `Failed: ${err}`;
        }
    });

    $('#prefs-send-report').addEventListener('click', async () => {
        await window.go.main.AudioNormalizer.SendLogReport();
    });
}

// ---------- runtime events ----------
function setupRuntimeEvents() {
    if (!window.runtime || !window.runtime.EventsOn) return;
    window.runtime.EventsOn('status:log', (msg) => appendLog(String(msg)));
    window.runtime.EventsOn('progress:update', (val) => setProgress(Number(val)));
    window.runtime.EventsOn('progress:done', () => onProcessingDone());
    window.runtime.EventsOn('file:added', (files) => renderFileList(files));
    window.runtime.EventsOn('update:available', (info) => {
        appendLog(`Update available: ${info && info.version ? info.version : 'see preferences'}`);
    });
    window.runtime.EventsOn('watch:file', (path) => appendLog(`Watch: ${path}`));
}

// ---------- init ----------
async function init() {
    setupLogo();
    setupTabs();
    setupButtons();
    setupRuntimeEvents();

    setLufsTpVisible(false);

    try {
        const formats = await window.go.main.AudioNormalizer.GetPlatformFormats();
        const sel = els.formatSelect();
        sel.innerHTML = '';
        formats.forEach((f) => {
            const opt = document.createElement('option');
            opt.value = f;
            opt.textContent = f;
            sel.appendChild(opt);
        });
        if (formats.length > 1) sel.value = formats[1];
        onFormatChange();
    } catch (err) {
        appendLog(`Failed to load platform formats: ${err}`);
    }

    try {
        state.metadataFields = await window.go.main.AudioNormalizer.MetadataFields();
        buildMetadataForm(state.metadataFields);
    } catch (err) {
        appendLog(`Failed to load metadata fields: ${err}`);
    }

    try {
        const version = await window.go.main.AudioNormalizer.GetVersion();
        $('#prefs-version-text').textContent = version;
    } catch (_) { /* ignore */ }

    try {
        const prefs = await window.go.main.AudioNormalizer.LoadPreferences();
        applyPrefsToUI(prefs);
    } catch (err) {
        appendLog(`Failed to load preferences: ${err}`);
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
            els.outputPath().textContent = out;
            els.outputPath().title = out;
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
    let last = null;
    setInterval(async () => {
        try {
            const r = await fetch('./reload-token.txt?_=' + Date.now(), { cache: 'no-store' });
            if (!r.ok) return;
            const txt = (await r.text()).trim();
            if (last !== null && txt !== last) {
                location.reload();
                return;
            }
            last = txt;
        } catch (_) { /* offline / 404 — ignore */ }
    }, 500);
})();
