'use strict';

// TNT — Wails wiring. Talks to Go via window.go.main.AudioNormalizer.* and
// window.runtime.EventsOn. Pure UI helpers live in ui.js.

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
const FAST_FORMAT_MAP = {
  pcm:  'PCM',
  flac: 'FLAC',
  aac:  'AAC',
  mp3:  'MPEG-II L3',
  opus: 'Opus',
};

const FORMAT_LABELS = {
  'PCM':              'PCM (WAV)',
  'FLAC':             'FLAC',
  'AAC':              'AAC',
  'AAC (Fraunhofer)': 'AAC — Fraunhofer FDK',
  'AAC (Apple)':      'AAC — Apple AudioToolbox',
  'MPEG-II L3':       'MP3',
  'Opus':             'Opus',
};

async function populateFormatDropdown() {
  const sel = $('adv-format');
  if (!sel) return;
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

const NORM_STD_MAP = {
  ebu:    'EBU R128 (-23 LUFS)',
  atsc:   'USA ATSC A/85 (-24 LUFS)',
  custom: 'Custom',
};
const NORM_STD_REVERSE = {
  'EBU R128 (-23 LUFS)':     'ebu',
  'USA ATSC A/85 (-24 LUFS)': 'atsc',
  'Custom':                   'custom',
};

const META_MAP = {
  'mt-title':   'title',
  'mt-artist':  'artist',
  'mt-album':   'album',
  'mt-year':    'date',
  'mt-comment': 'comment',
  'mt-track':   'track',
};

/* ── Helpers ── */
function basename(path) {
  if (!path) return '';
  const parts = String(path).split(/[\\/]/);
  return parts[parts.length - 1];
}
function ext(path) {
  const m = /\.([^./\\]+)$/.exec(String(path));
  return m ? m[1].toUpperCase() : '';
}
function parseSR(s) { return (s || '').replace(/\D/g, ''); }
function parseBD(s) {
  const t = (s || '').toLowerCase();
  if (t.startsWith('16')) return '16';
  if (t.startsWith('24')) return '24';
  if (t.startsWith('32')) return '32 (float)';
  if (t.startsWith('64')) return '64 (float)';
  return '24';
}

/* ── File list rendering ── */
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
      remove.innerHTML = '&times;';

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
  const proc = $('btn-process');
  const clr  = $('btn-clear');
  if (proc) proc.disabled = !has || state.processing;
  if (clr)  clr.disabled  = !has;
  updateMetaTab();
}

function updateMetaTab() {
  const path = (state.selectedIdx != null) ? state.files[state.selectedIdx] : null;
  const hint = $('meta-hint');
  if (hint) {
    hint.textContent = path
      ? `Editing: ${basename(path)}`
      : 'Select a single file in the queue to edit its metadata.';
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
  const r = $('meta-read');
  const w = $('meta-write');
  if (r) r.disabled = !has;
  if (w) w.disabled = !has;
}

/* ── Inline-handler entry points ── */
// Both "+ Files" and "+ Folder" inline-call this; dispatch by button text.
async function addMockFile() {
  const t = (window.event && window.event.currentTarget && window.event.currentTarget.textContent || '').toLowerCase();
  try {
    const files = t.includes('folder')
      ? await window.go.main.AudioNormalizer.SelectFolder()
      : await window.go.main.AudioNormalizer.SelectFiles();
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
      if (out) { out.textContent = dir; out.title = dir; }
      addLog('Output folder set', 'info');
    }
  } catch (err) {
    addLog('Output folder failed: ' + err, 'err');
  }
}

async function handleProcess() {
  if (!state.files.length || state.processing) return;
  state.processing = true;
  $('btn-process').disabled = true;
  setProgress(0);
  try {
    await window.go.main.AudioNormalizer.Process(buildConfig());
  } catch (err) {
    addLog('Process failed: ' + err, 'err');
    state.processing = false;
    clearProgress();
    $('btn-process').disabled = state.files.length === 0;
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
function buildConfig() {
  const activeTab = document.querySelector('.tab-btn.active')?.dataset.tab || 'fast';

  const proc = {
    DynamicsPreset: $('proc-dyn') ? $('proc-dyn').value : 'Off',
    EqTarget:       $('proc-eq')  ? $('proc-eq').value  : 'Off',
    DynNorm:        $('proc-dn')  ? $('proc-dn').checked : false,
    BypassProc:     $('proc-bypass') ? $('proc-bypass').checked : false,
    PhaseCheck:     $('prefs-phase') ? $('prefs-phase').checked : false,
  };

  if (activeTab === 'fast') {
    const preset = document.querySelector('input[name="fast-preset"]:checked')?.value || 'pcm';
    const cfg = {
      ...proc,
      Format: '', SampleRate: '', BitDepth: '', Bitrate: '',
      UseLoudnorm:    $('fast-norm') ? $('fast-norm').checked : false,
      CustomLoudnorm: false,
      IsSpeech:       false,
      WriteTags:      false,
      NoTranscode:    false,
      OriginIsAAC:    false,
      DataCompLevel:  0,
    };
    switch (preset) {
      case 'aac': cfg.Format = FAST_FORMAT_MAP.aac; cfg.Bitrate = '256'; break;
      case 'mp3': cfg.Format = FAST_FORMAT_MAP.mp3; cfg.Bitrate = '320'; break;
      case 'pcm':
      default:    cfg.Format = FAST_FORMAT_MAP.pcm; cfg.SampleRate = '48000'; cfg.BitDepth = '24'; break;
    }
    return cfg;
  }

  return {
    ...proc,
    Format:         $('adv-format') ? $('adv-format').value : 'PCM',
    SampleRate:     parseSR($('adv-sr') ? $('adv-sr').value : ''),
    BitDepth:       parseBD($('adv-bd') ? $('adv-bd').value : ''),
    Bitrate:        $('adv-br') ? $('adv-br').value : '',
    UseLoudnorm:    $('adv-norm') ? $('adv-norm').checked : false,
    CustomLoudnorm: $('adv-custom-loud') ? $('adv-custom-loud').checked : false,
    IsSpeech:       $('adv-speech') ? $('adv-speech').checked : false,
    WriteTags:      $('adv-rg') ? $('adv-rg').checked : false,
    NoTranscode:    $('adv-no-transcode') ? $('adv-no-transcode').checked : false,
    OriginIsAAC:    false,
    DataCompLevel:  parseInt($('adv-cl') ? $('adv-cl').value : '0', 10),
  };
}

/* ── Metadata read/write ── */
function setRGRow(gain, target) {
  const rows = document.querySelectorAll('.rg-row');
  const showGain   = !!gain;
  const showTarget = !!target;
  rows.forEach((el) => {
    const isGain   = el.id === 'mt-rg-gain'   || el.htmlFor === 'mt-rg-gain';
    const isTarget = el.id === 'mt-rg-target' || el.htmlFor === 'mt-rg-target';
    if (isGain)   el.classList.toggle('hidden', !showGain);
    if (isTarget) el.classList.toggle('hidden', !showTarget);
  });
  const g = $('mt-rg-gain');   if (g) g.value = gain   || '';
  const t = $('mt-rg-target'); if (t) t.value = target || '';
}

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
      const el = $(id);
      if (el) el.value = (tags && tags[key]) || '';
    });

    // Track may be "5" or "5/12"; show total separately, read-only.
    const trackRaw = (tags && tags.track) || '';
    const [cur, total] = String(trackRaw).split('/').map((s) => s.trim());
    if ($('mt-track'))       $('mt-track').value       = cur || '';
    if ($('mt-track-total')) $('mt-track-total').value = total || '';
    const sep = $('mt-track-sep');
    if (sep) sep.style.visibility = total ? 'visible' : 'hidden';
    const tot = $('mt-track-total');
    if (tot) tot.style.visibility = total ? 'visible' : 'hidden';

    // ReplayGain — read-only display, hidden if absent.
    const gain   = pickFirst(tags, ['replaygain_track_gain', 'replaygain_album_gain']);
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
  const tags = {};
  Object.entries(META_MAP).forEach(([id, key]) => {
    if (id === 'mt-track') return;
    const el = $(id);
    if (el) tags[key] = el.value;
  });
  // Recombine track as "X/N" if total is present.
  const cur   = $('mt-track')       ? $('mt-track').value.trim()       : '';
  const total = $('mt-track-total') ? $('mt-track-total').value.trim() : '';
  tags.track = total ? `${cur}/${total}` : cur;
  try {
    await window.go.main.AudioNormalizer.WriteMetadata(path, tags);
    addLog('Tags written to ' + basename(path), 'ok');
  } catch (err) {
    addLog('Write tags failed: ' + err, 'err');
  }
}

/* ── Preferences sync ── */
function applyPrefsToUI(prefs) {
  if (!prefs) return;
  state.normalizationStandard = prefs.NormalizationStandard || 'EBU R128 (-23 LUFS)';

  if (prefs.SimpleMode) {
    const r = document.querySelector(`input[name="fast-preset"][value="${prefs.SimpleMode}"]`);
    if (r) r.checked = true;
  }
  if (prefs.Format) {
    const sel = $('adv-format');
    if (sel && Array.from(sel.options).some((o) => o.value === prefs.Format)) {
      sel.value = prefs.Format;
    }
  }
  if (prefs.SampleRate) {
    const sel = $('adv-sr');
    if (sel) {
      const want = parseSR(prefs.SampleRate);
      Array.from(sel.options).forEach((o) => { if (parseSR(o.textContent) === want) sel.value = o.value || o.textContent; });
    }
  }
  if (prefs.BitDepth) {
    const sel = $('adv-bd');
    if (sel) {
      Array.from(sel.options).forEach((o) => { if (parseBD(o.textContent) === parseBD(prefs.BitDepth)) sel.value = o.value || o.textContent; });
    }
  }
  if (prefs.Bitrate)         { const el = $('adv-br'); if (el) el.value = prefs.Bitrate; }
  if (prefs.NormalizeTarget) { const el = $('adv-lufs'); if (el) el.value = prefs.NormalizeTarget; }
  if (prefs.NormalizeTargetTp) { const el = $('adv-tp'); if (el) el.value = prefs.NormalizeTargetTp; }
  if (typeof prefs.LoudnormEnabled === 'boolean') {
    if ($('fast-norm')) $('fast-norm').checked = prefs.LoudnormEnabled;
    if ($('adv-norm'))  $('adv-norm').checked  = prefs.LoudnormEnabled;
  }
  if (typeof prefs.CustomLoudnorm === 'boolean') {
    if ($('adv-custom-loud')) $('adv-custom-loud').checked = prefs.CustomLoudnorm;
  }
  if (typeof prefs.DataCompLevel === 'number') {
    if ($('adv-cl'))     $('adv-cl').value     = prefs.DataCompLevel;
    if ($('adv-cl-out')) $('adv-cl-out').value = prefs.DataCompLevel;
  }
  if (prefs.EqPreset)  { const el = $('proc-eq');  if (el) el.value = prefs.EqPreset; }
  if (prefs.DynPreset) { const el = $('proc-dyn'); if (el) el.value = prefs.DynPreset; }
  if (typeof prefs.DynNorm    === 'boolean' && $('proc-dn'))     $('proc-dn').checked     = prefs.DynNorm;
  if (typeof prefs.PhaseCheck === 'boolean' && $('prefs-phase')) $('prefs-phase').checked = prefs.PhaseCheck;
  if (prefs.LastOutputDir) {
    const out = $('output-path');
    if (out) { out.textContent = prefs.LastOutputDir; out.title = prefs.LastOutputDir; }
  }

  const stdKey = NORM_STD_REVERSE[state.normalizationStandard];
  if (stdKey) {
    const r = document.querySelector(`input[name="norm-std"][value="${stdKey}"]`);
    if (r) r.checked = true;
  }
  if ($('prefs-lufs')) $('prefs-lufs').value = prefs.NormalizeTarget   || '-23';
  if ($('prefs-tp'))   $('prefs-tp').value   = prefs.NormalizeTargetTp || '-1';

  if (prefs.SelectedTab) switchTab(prefs.SelectedTab);
  updateAdvanced();
  updateBypass($('proc-bypass') ? $('proc-bypass').checked : false);
}

function gatherPrefs() {
  const stdKey  = document.querySelector('input[name="norm-std"]:checked')?.value || 'ebu';
  const std     = NORM_STD_MAP[stdKey] || state.normalizationStandard;
  const simple  = document.querySelector('input[name="fast-preset"]:checked')?.value || '';
  const tab     = document.querySelector('.tab-btn.active')?.dataset.tab || 'fast';

  return {
    AdvancedMode:          tab === 'advanced' || tab === 'processing',
    LastOutputDir:         ($('output-path') && $('output-path').title) || '',
    SimpleMode:            simple,
    Format:                $('adv-format') ? $('adv-format').value : '',
    SampleRate:            parseSR($('adv-sr') ? $('adv-sr').value : ''),
    BitDepth:              parseBD($('adv-bd') ? $('adv-bd').value : ''),
    Bitrate:               $('adv-br') ? $('adv-br').value : '',
    LoudnormEnabled:       $('adv-norm') ? $('adv-norm').checked : false,
    CustomLoudnorm:        $('adv-custom-loud') ? $('adv-custom-loud').checked : false,
    NormalizeTarget:       stdKey === 'custom' ? ($('prefs-lufs') ? $('prefs-lufs').value : '-23')
                                                : ($('adv-lufs')   ? $('adv-lufs').value   : '-23'),
    NormalizeTargetTp:     stdKey === 'custom' ? ($('prefs-tp')   ? $('prefs-tp').value   : '-1')
                                                : ($('adv-tp')     ? $('adv-tp').value     : '-1'),
    NormalizationStandard: std,
    DataCompLevel:         parseInt($('adv-cl') ? $('adv-cl').value : '0', 10),
    EqPreset:              $('proc-eq')  ? $('proc-eq').value  : 'Off',
    DynPreset:             $('proc-dyn') ? $('proc-dyn').value : 'Off',
    DynNorm:               $('proc-dn')  ? $('proc-dn').checked : false,
    SelectedTab:           tab,
    PhaseCheck:            $('prefs-phase') ? $('prefs-phase').checked : false,
  };
}

/* ── Override inline mock handlers and bind extra listeners ── */
function bindRealHandlers() {
  // Metadata buttons — replace inline addLog stubs
  const r = $('meta-read');  if (r) { r.onclick = null; r.addEventListener('click', readMetadataIntoForm); }
  const w = $('meta-write'); if (w) { w.onclick = null; w.addEventListener('click', writeMetadataFromForm); }

  // Preferences pane buttons
  const prefsSavePane  = $('prefs-save');
  if (prefsSavePane) {
    const [saveBtn, resetBtn] = prefsSavePane.querySelectorAll('button');
    if (saveBtn)  { saveBtn.onclick  = null; saveBtn.addEventListener('click', async () => {
      try { await window.go.main.AudioNormalizer.SavePreferences(gatherPrefs()); addLog('Configuration saved', 'ok'); }
      catch (err) { addLog('Save failed: ' + err, 'err'); }
    }); }
    if (resetBtn) { resetBtn.onclick = null; resetBtn.addEventListener('click', async () => {
      try { await window.go.main.AudioNormalizer.ResetPreferences();
            const prefs = await window.go.main.AudioNormalizer.LoadPreferences();
            applyPrefsToUI(prefs);
            addLog('Settings reset to defaults', 'info'); }
      catch (err) { addLog('Reset failed: ' + err, 'err'); }
    }); }
  }

  const versionPane = $('prefs-version');
  if (versionPane) {
    const checkBtn = versionPane.querySelector('button');
    if (checkBtn) { checkBtn.onclick = null; checkBtn.addEventListener('click', async () => {
      try {
        const v = await window.go.main.AudioNormalizer.CheckForUpdates();
        if (v && v.version) addLog('Update available: ' + v.version, 'info');
        else                addLog('You are up to date', 'ok');
      } catch (err) { addLog('Update check failed: ' + err, 'err'); }
    }); }
  }

  const reportPane = $('prefs-report');
  if (reportPane) {
    const sendBtn = reportPane.querySelector('button');
    if (sendBtn) { sendBtn.onclick = null; sendBtn.addEventListener('click', async () => {
      try { await window.go.main.AudioNormalizer.SendLogReport(); addLog('Error report sent', 'info'); }
      catch (err) { addLog('Send report failed: ' + err, 'err'); }
    }); }
  }

  // Watch mode toggle
  const watch = $('prefs-watch-mode');
  if (watch) {
    watch.addEventListener('change', async (e) => {
      try {
        if (e.target.checked) {
          await window.go.main.AudioNormalizer.StartWatching();
          state.watching = true;
          const warn = $('watcher-warn'); if (warn) warn.textContent = 'WATCHING';
          addLog('Watch mode enabled', 'info');
        } else {
          await window.go.main.AudioNormalizer.StopWatching();
          state.watching = false;
          const warn = $('watcher-warn'); if (warn) warn.textContent = '';
          addLog('Watch mode disabled', 'info');
        }
      } catch (err) {
        addLog('Watch toggle failed: ' + err, 'err');
        e.target.checked = state.watching;
      }
    });
  }

  // Norm-std radios → push into adv-lufs/adv-tp + keep state in sync
  document.querySelectorAll('input[name="norm-std"]').forEach((r) => {
    r.addEventListener('change', () => {
      const key = r.value;
      state.normalizationStandard = NORM_STD_MAP[key] || state.normalizationStandard;
      if (key === 'ebu')  { setLT('-23', '-1'); }
      if (key === 'atsc') { setLT('-24', '-2'); }
      // 'custom' uses whatever is in prefs-lufs/prefs-tp
    });
  });

  // Speech toggle forces Opus
  const sp = $('adv-speech');
  if (sp) sp.addEventListener('change', (e) => {
    if (e.target.checked) {
      const sel = $('adv-format');
      if (sel) { sel.value = 'opus'; updateAdvanced(); }
    }
  });

  // Custom loudness onchange already fires updateAdvanced via inline; nothing extra.

  // Bypass already handled by ui.js updateBypass; nothing extra.
}

function setLT(lufs, tp) {
  if ($('adv-lufs'))   $('adv-lufs').value   = lufs;
  if ($('adv-tp'))     $('adv-tp').value     = tp;
  if ($('prefs-lufs')) $('prefs-lufs').value = lufs;
  if ($('prefs-tp'))   $('prefs-tp').value   = tp;
}

/* ── Wails runtime events ── */
function setupRuntimeEvents() {
  if (!window.runtime || !window.runtime.EventsOn) return;
  window.runtime.EventsOn('status:log',     (msg) => addLog(String(msg), 'info'));
  window.runtime.EventsOn('progress:update', (val) => setProgress(Number(val)));
  window.runtime.EventsOn('progress:done',  () => {
    state.processing = false;
    clearProgress();
    if ($('btn-process')) $('btn-process').disabled = state.files.length === 0;
    addLog('Processing complete', 'ok');
  });
  window.runtime.EventsOn('file:added',     (files) => renderFileList(files || []));
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
    document.querySelectorAll('#prefs-version p').forEach((p) => {
      p.textContent = 'You are running version ' + version + '.';
    });
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
      if (el) { el.textContent = out; el.title = out; }
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
      if (last !== null && txt !== last) { location.reload(); return; }
      last = txt;
    } catch (_) { /* offline / 404 */ }
  }, 500);
})();
