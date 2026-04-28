'use strict';

/* ── State ── */
let files = [
  { id:1, name:'Montage_v3_final.wav',  ext:'WAV', info:'48 kHz · 24-bit · stereo · 4:32' },
  { id:2, name:'Interview_raw.aif',     ext:'AIF', info:'44.1 kHz · 16-bit · stereo · 12:07' },
  { id:3, name:'Outro_music.mp3',       ext:'MP3', info:'44.1 kHz · 320 kbps · stereo · 1:48' },
];
let selectedId = 1;
let nextId     = 10;
let processing = false;

const MOCK_NAMES = [
  ['Report_final.wav',   'WAV', '48 kHz · 24-bit · mono · 3:00'],
  ['Show_edit_v2.aif',   'AIF', '44.1 kHz · 16-bit · stereo · 8:00'],
  ['Bumper_60s.mp3',     'MP3', '44.1 kHz · 192 kbps · stereo · 1:00'],
  ['Promo_30s.wav',      'WAV', '48 kHz · 24-bit · mono · 0:30'],
];

const $ = id => document.getElementById(id);

/* ── Theme ── */
function setTheme(mode) {
  document.documentElement.classList.remove('light', 'dark');
  document.documentElement.classList.add(mode);
  $('theme-btn-light').classList.toggle('active', mode === 'light');
  $('theme-btn-dark').classList.toggle('active', mode === 'dark');
}

(function initTheme() {
  const dark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  $(dark ? 'theme-btn-dark' : 'theme-btn-light').classList.add('active');
})();

/* ── Main tabs ── */
function switchTab(id) {
  document.querySelectorAll('.tab-btn').forEach(b =>
    b.classList.toggle('active', b.dataset.tab === id));
  document.querySelectorAll('.tab-pane').forEach(p =>
    p.classList.toggle('active', p.id === 'tab-' + id));
  $('audio-chain-group').classList.toggle('chain-active',
    id === 'advanced' || id === 'processing');
}

/* ── Dialog tabs ── */
function switchDialogTab(btn, navId, bodyId) {
  document.querySelectorAll('#' + navId + ' .dialog-tab-btn').forEach(b =>
    b.classList.toggle('active', b === btn));
  const target = btn.dataset.panel;
  document.querySelectorAll('#' + bodyId + ' .dialog-pane').forEach(p =>
    p.classList.toggle('active', p.id === target));
}

/* ── Dialogs ── */
function openDialog(id)  { $(id).showModal(); }
function closeDialog(id) { $(id).close(); }

document.querySelectorAll('dialog').forEach(dlg => {
  dlg.addEventListener('click', e => {
    const r = dlg.getBoundingClientRect();
    if (e.clientX < r.left || e.clientX > r.right ||
        e.clientY < r.top  || e.clientY > r.bottom) dlg.close();
  });
});

/* ── File list ── */
function renderFileList() {
  const list = $('file-list');
  list.innerHTML = '';

  if (!files.length) {
    const empty = document.createElement('div');
    empty.className = 'file-empty';
    empty.textContent = 'Drop audio files here\nor use the buttons above';
    list.appendChild(empty);
  } else {
    files.forEach(f => {
      const item = document.createElement('div');
      item.className = 'file-item' + (f.id === selectedId ? ' selected' : '');
      item.innerHTML =
        `<div class="file-badge"><span>${f.ext}</span></div>` +
        `<div class="file-info">` +
          `<div class="file-name">${f.name}</div>` +
          `<div class="file-meta">${f.info}</div>` +
        `</div>` +
        `<button class="file-remove" title="Remove">&times;</button>`;
      item.addEventListener('click', () => { selectedId = f.id; renderFileList(); });
      item.querySelector('.file-remove').addEventListener('click', e => {
        e.stopPropagation();
        files = files.filter(x => x.id !== f.id);
        if (selectedId === f.id) selectedId = files.length ? files[0].id : null;
        renderFileList();
      });
      list.appendChild(item);
    });
  }

  const has = files.length > 0;
  $('btn-process').disabled = !has || processing;
  $('btn-clear').disabled   = !has;
  updateMetaTab();
}

function addMockFile() {
  const [name, ext, info] = MOCK_NAMES[nextId % MOCK_NAMES.length];
  files.push({ id: nextId++, name, ext, info });
  renderFileList();
}

function clearFiles() {
  files = []; selectedId = null; renderFileList();
}

function updateMetaTab() {
  const f = files.find(x => x.id === selectedId);
  $('meta-hint').textContent = f
    ? `Editing: ${f.name}`
    : 'Select a single file in the queue to edit its metadata.';
  const has = !!f;
  $('meta-grid').classList.toggle('disabled', !has);
  $('meta-grid').querySelectorAll('input').forEach(i => i.disabled = !has);
  $('meta-read').disabled  = !has;
  $('meta-write').disabled = !has;
}

/* ── Output ── */
function setOutput() {
  $('output-path').textContent = '~/Music/TNT Output';
  addLog('Output folder set', 'info');
}

/* ── Process ── */
function handleProcess() {
  if (!files.length || processing) return;
  processing = true;
  $('btn-process').disabled = true;
  $('progress-track').classList.add('visible');
  addLog(`Processing ${files.length} file${files.length > 1 ? 's' : ''}…`, 'info');
  files.forEach((f, i) =>
    setTimeout(() => addLog(`${f.name} complete`, 'ok'), 900 + i * 1100));
  setTimeout(() => {
    processing = false;
    $('progress-track').classList.remove('visible');
    if (files.length) $('btn-process').disabled = false;
  }, 900 + files.length * 1100 + 200);
}

function previewSize() {
  const mb = (files.length * 72.8).toFixed(1);
  addLog(`Estimated output: ~${mb} MB for ${files.length} file${files.length !== 1 ? 's' : ''}`, 'info');
}

/* ── Advanced conditional fields ── */
function updateAdvanced() {
  const fmt    = $('adv-format').value;
  const isPCM  = fmt === 'pcm';
  const isFLAC = fmt === 'flac';
  const isOpus = fmt === 'opus';
  const hasRate = ['aac', 'mp3', 'opus'].includes(fmt);

  const toggle = (id, show) => { const el = $(id); if (el) el.classList.toggle('hidden', !show); };
  toggle('row-sr',     isPCM);
  toggle('row-bd',     isPCM);
  toggle('row-br',     hasRate);
  toggle('row-cl',     isFLAC || isOpus);
  toggle('row-speech', isOpus);

  const rgRow = $('row-rg');
  if (rgRow) {
    rgRow.classList.toggle('dimmed', isPCM);
    if (isPCM) rgRow.querySelector('input').checked = false;
  }
}

/* ── Processing bypass ── */
function updateBypass(on) {
  ['proc-row-dyn', 'proc-row-eq', 'proc-row-dn'].forEach(id => {
    const el = $(id);
    if (el) el.classList.toggle('dimmed', on);
  });
}

/* ── Log ── */
function addLog(text, type = 'ok') {
  const bar = $('log-bar');
  const ready = bar.querySelector('.log-ready');
  if (ready) ready.remove();

  const el = document.createElement('span');
  el.className = `log-entry log-${type}`;
  el.textContent = text;
  bar.appendChild(el);

  setTimeout(() => expireLog(el), 3800);
}

function expireLog(el) {
  if (!el.isConnected) return;

  // Pin rendered width so collapse has a defined start value
  el.style.width      = el.getBoundingClientRect().width + 'px';
  el.style.flexShrink = '0';
  void el.offsetWidth; // force reflow

  // Phase 1 — fade opacity
  el.style.transition = 'opacity 0.3s ease';
  el.style.opacity    = '0';

  // Phase 2 — collapse width after opacity finishes
  setTimeout(() => {
    el.style.transition   = 'width 0.25s ease, padding-right 0.25s ease';
    el.style.width        = '0';
    el.style.paddingRight = '0';

    setTimeout(() => {
      el.remove();
      const bar = $('log-bar');
      if (bar && !bar.querySelector('.log-entry')) {
        const r = document.createElement('span');
        r.className   = 'log-ready';
        r.textContent = 'Ready';
        bar.appendChild(r);
      }
    }, 260);
  }, 320);
}

/* ── Init ── */
renderFileList();
updateAdvanced();
