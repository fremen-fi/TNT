'use strict';

// Pure UI helpers — no Wails calls. Functions are global so the inline
// onclick/onchange attributes in index.html can find them.

const $ = (id) => document.getElementById(id);

/* ── Theme ── */
function setTheme(mode) {
  document.documentElement.classList.remove('light', 'dark');
  document.documentElement.classList.add(mode);
  $('theme-btn-light').classList.toggle('active', mode === 'light');
  $('theme-btn-dark').classList.toggle('active', mode === 'dark');
}

(function initTheme() {
  const dark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  const id = dark ? 'theme-btn-dark' : 'theme-btn-light';
  document.addEventListener('DOMContentLoaded', () => {
    const el = $(id);
    if (el) el.classList.add('active');
  });
})();

/* ── Main tabs ── */
function switchTab(id) {
  document.querySelectorAll('.tab-btn').forEach((b) =>
    b.classList.toggle('active', b.dataset.tab === id));
  document.querySelectorAll('.tab-pane').forEach((p) =>
    p.classList.toggle('active', p.id === 'tab-' + id));
  const chain = $('audio-chain-group');
  if (chain) chain.classList.toggle('chain-active', id === 'advanced' || id === 'processing');
}

/* ── Dialog tabs ── */
function switchDialogTab(btn, navId, bodyId) {
  document.querySelectorAll('#' + navId + ' .dialog-tab-btn').forEach((b) =>
    b.classList.toggle('active', b === btn));
  const target = btn.dataset.panel;
  document.querySelectorAll('#' + bodyId + ' .dialog-pane').forEach((p) =>
    p.classList.toggle('active', p.id === target));
}

/* ── Dialogs ── */
function openDialog(id)  { const d = $(id); if (d) d.showModal(); }
function closeDialog(id) { const d = $(id); if (d) d.close(); }

document.addEventListener('DOMContentLoaded', () => {
  document.querySelectorAll('dialog').forEach((dlg) => {
    dlg.addEventListener('click', (e) => {
      const r = dlg.getBoundingClientRect();
      if (e.clientX < r.left || e.clientX > r.right ||
          e.clientY < r.top  || e.clientY > r.bottom) dlg.close();
    });
  });
});

/* ── Advanced format show/hide ── */
function formatKind(v) {
  const s = String(v || '');
  if (s.startsWith('PCM'))  return 'pcm';
  if (s.startsWith('FLAC')) return 'flac';
  if (s.startsWith('AAC'))  return 'aac';
  if (s.startsWith('MPEG')) return 'mp3';
  if (s.startsWith('Opus')) return 'opus';
  return s.toLowerCase();
}

function updateAdvanced() {
  const fmtSel = $('adv-format');
  if (!fmtSel) return;
  const kind   = formatKind(fmtSel.value);
  const isPCM  = kind === 'pcm';
  const isFLAC = kind === 'flac';
  const isOpus = kind === 'opus';
  const hasRate = ['aac', 'mp3', 'opus'].includes(kind);

  const toggle = (id, show) => { const el = $(id); if (el) el.classList.toggle('hidden', !show); };
  toggle('row-sr',     isPCM);
  toggle('row-bd',     isPCM);
  toggle('row-br',     hasRate);
  toggle('row-cl',     isFLAC || isOpus);
  toggle('row-speech', isOpus);

  const customLoud = $('adv-custom-loud');
  const customOn = !!(customLoud && customLoud.checked);
  toggle('row-lufs', customOn);
  toggle('row-tp',   customOn);

  const rgRow = $('row-rg');
  if (rgRow) {
    rgRow.classList.toggle('dimmed', isPCM);
    if (isPCM) {
      const rg = rgRow.querySelector('input');
      if (rg) rg.checked = false;
    }
  }
}

/* ── Processing bypass dim ── */
function updateBypass(on) {
  ['proc-row-dyn', 'proc-row-eq', 'proc-row-dn'].forEach((id) => {
    const el = $(id);
    if (el) el.classList.toggle('dimmed', on);
  });
  const dyn = $('proc-dyn');
  const eq  = $('proc-eq');
  const dn  = $('proc-dn');
  if (dyn) dyn.disabled = on;
  if (eq)  eq.disabled  = on;
  if (dn)  dn.disabled  = on;
}

/* ── Status log bar ── */
function addLog(text, type = 'ok') {
  const bar = $('log-bar');
  if (!bar) return;
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

  el.style.width      = el.getBoundingClientRect().width + 'px';
  el.style.flexShrink = '0';
  void el.offsetWidth;

  el.style.transition = 'opacity 0.3s ease';
  el.style.opacity    = '0';

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

/* ── Progress bar ── */
function setProgress(fraction) {
  const track = $('progress-track');
  if (!track) return;
  track.classList.add('visible');
  const fill = track.querySelector('.progress-fill');
  if (fill) fill.style.width = Math.max(0, Math.min(1, fraction)) * 100 + '%';
}

function clearProgress() {
  const track = $('progress-track');
  if (!track) return;
  track.classList.remove('visible');
  const fill = track.querySelector('.progress-fill');
  if (fill) fill.style.width = '0%';
}
