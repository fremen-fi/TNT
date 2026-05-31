'use strict';

// Pure UI helpers — no Wails calls. Functions are global so the inline
// onclick/onchange attributes in index.html can find them.

/** @param {string} id */
const $ = (id) => document.getElementById(id);
// Typed lookups so TS knows .checked/.value/.disabled are valid.
/** @param {string} id @returns {HTMLInputElement|null}  */
const $input = (id) => /** @type {HTMLInputElement|null}  */ (document.getElementById(id));
/** @param {string} id @returns {HTMLSelectElement|null} */
const $select = (id) => /** @type {HTMLSelectElement|null} */ (document.getElementById(id));
/** @param {string} id @returns {HTMLButtonElement|null} */
const $button = (id) => /** @type {HTMLButtonElement|null} */ (document.getElementById(id));

/* ── Theme ── */
/** @param {'light'|'dark'} mode */
function setTheme(mode) {
    document.documentElement.classList.remove('light', 'dark');
    document.documentElement.classList.add(mode);
    $('theme-btn-light')?.classList.toggle('active', mode === 'light');
    $('theme-btn-dark')?.classList.toggle('active', mode === 'dark');
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
/** @param {string} id */
function switchTab(id) {
    document.querySelectorAll('.tab-btn').forEach((b) => {
        const el = /** @type {HTMLElement} */ (b);
        el.classList.toggle('active', el.dataset.tab === id);
    });
    document.querySelectorAll('.tab-pane').forEach((p) =>
        p.classList.toggle('active', p.id === 'tab-' + id));
    const chain = $('audio-chain-group');
    if (chain) chain.classList.toggle('chain-active', id === 'advanced' || id === 'processing');
    updateChainIndicator();
    updateDitherIndicator();
}

/* ── Dialog tabs ── */
/** @param {HTMLElement} btn @param {string} navId @param {string} bodyId */
function switchDialogTab(btn, navId, bodyId) {
    document.querySelectorAll('#' + navId + ' .dialog-tab-btn').forEach((b) =>
        b.classList.toggle('active', b === btn));
    const target = btn.dataset.panel;
    document.querySelectorAll('#' + bodyId + ' .dialog-pane').forEach((p) =>
        p.classList.toggle('active', p.id === target));
}

/* ── Dialogs ── */
/** @param {string} id */
function openDialog(id) { const d = /** @type {HTMLDialogElement|null} */ ($(id)); if (d) d.showModal(); }
/** @param {string} id */
function closeDialog(id) { const d = /** @type {HTMLDialogElement|null} */ ($(id)); if (d) d.close(); }

document.addEventListener('DOMContentLoaded', () => {
    document.querySelectorAll('dialog').forEach((dlg) => {
        dlg.addEventListener('click', (e) => {
            const r = dlg.getBoundingClientRect();
            if (e.clientX < r.left || e.clientX > r.right ||
                e.clientY < r.top || e.clientY > r.bottom) dlg.close();
        });
    });
});

/* ── Advanced format show/hide ── */
/** @param {string} v @returns {'pcm'|'flac'|'aac'|'mp3'|'opus'|string} */
function formatKind(v) {
    const s = String(v || '');
    if (s.startsWith('PCM')) return 'pcm';
    if (s.startsWith('FLAC')) return 'flac';
    if (s.startsWith('AAC')) return 'aac';
    if (s.startsWith('MPEG')) return 'mp3';
    if (s.startsWith('Opus')) return 'opus';
    return s.toLowerCase();
}

function updateAdvanced() {
    const fmtSel = $select('adv-format');
    if (!fmtSel) return;
    const kind = formatKind(fmtSel.value);
    const isPCM = kind === 'pcm';
    const isFLAC = kind === 'flac';
    const isOpus = kind === 'opus';
    const hasRate = ['aac', 'mp3', 'opus'].includes(kind);

    /** @param {string} id @param {boolean} show */
    const toggle = (id, show) => { const el = $(id); if (el) el.classList.toggle('hidden', !show); };
    toggle('row-sr', isPCM);
    toggle('row-bd', isPCM);
    toggle('row-br', hasRate);
    toggle('row-cl', isFLAC || isOpus);
    //toggle('row-speech', isOpus);

    const customLoud = $input('adv-custom-loud');
    const customOn = !!(customLoud && customLoud.checked);
    toggle('row-lufs', customOn);
    toggle('row-tp', customOn);

    // disable RG
    // if Normalizing, and vice versa.
    // Since neither can co-exist.
    const rgRow = $('row-rg');
    const rgBox = $input('adv-rg');
    const normRow = $('row-norm');
    const normBox = $input('adv-norm');
    const willNormalize = !!(normBox && normBox.checked);
    const willRGTag = !!(rgBox && rgBox.checked);

    // Speech now toggles different targets for audio when AES77-2023 is used,
    // so it's now enabled for all encoders when
    // 1. Normalization is enabled and
    // 2. AES77-2023 is selected
    // OR if Opus is selected regardless of normalization or standard.
    const speechRow = $('row-speech');
    const speechBox = $input('adv-speech');
    const stdEl = /** @type {HTMLInputElement|null} */ (document.querySelector('input[name="norm-std"]:checked'));
    const isNormalizingAndAes = !!(normBox && normBox.checked) && !!stdEl && stdEl.value === 'aes77';

    if (speechRow) speechRow.classList.toggle('hidden', (!isNormalizingAndAes));
    if (speechBox) {
        speechBox.disabled = (!isNormalizingAndAes);
        if (!isNormalizingAndAes && !isOpus) speechBox.checked = false;
    }

    const rgBlocked = willNormalize || isPCM;
    if (rgRow) rgRow.classList.toggle('dimmed', rgBlocked);
    if (rgBox) {
        rgBox.disabled = rgBlocked;
        if (rgBlocked) rgBox.checked = false;
    }

    if (normRow) normRow.classList.toggle('dimmed', willRGTag);
    if (normBox) {
        normBox.disabled = willRGTag;
        if (willRGTag) normBox.checked = false;
    }

    // disable no-transcode for codecs that don't have any meaningful purpose in TNT without transcoding
    // e.g. encoding formats that can't hold metadata in a meaningful way
    const transCodeRow = $('row-transcode');
    const transCodeBox = $input('adv-no-transcode');
    const notTranscodeEncoder = isPCM || isFLAC;
    const tcBlocked = notTranscodeEncoder || willNormalize;
    if (transCodeRow) transCodeRow.classList.toggle('dimmed', tcBlocked);
    if (transCodeBox) {
        transCodeBox.disabled = tcBlocked;
        if (tcBlocked) transCodeBox.checked = false;
    }

    updateDitherIndicator();
}

/* ── Audio-chain active indicator (left-footer) ──
   Shows a small accent badge below the action row when EQ or Dynamics
   processing is active, but only on tabs where the user can actually see
   those controls (Advanced/Processing/Metadata). The point is to remind
   the user that processing settings are about to be applied — even when
   they're back on a tab that doesn't expose the controls. Hidden also
   when "Bypass all processing" is checked, since nothing applies then. */
function updateChainIndicator() {
    const ind = $('chain-indicator');
    if (!ind) return;
    const activeBtn = /** @type {HTMLElement|null} */ (document.querySelector('.tab-btn.active'));
    const activeTab = (activeBtn && activeBtn.dataset.tab) || 'fast';
    const dyn = $select('proc-dyn');
    const eq = $select('proc-eq');
    const byp = $input('proc-bypass');
    const dynVal = (dyn && dyn.value) || 'Off';
    const eqVal = (eq && eq.value) || 'Off';
    const dynOn = dynVal !== 'Off';
    const eqOn = eqVal !== 'Off';
    const bypassed = !!(byp && byp.checked);
    const show = (dynOn || eqOn) && activeTab !== 'fast' && !bypassed;
    ind.classList.toggle('hidden', !show);
    if (show) {
        /** @type {string[]} */
        const parts = [];
        if (eqOn) parts.push(`EQ: ${eqVal}`);
        if (dynOn) parts.push(`Dynamics: ${dynVal}`);
        ind.textContent = `Audio chain active — ${parts.join(' · ')}`;
    }
}

/* ── Dither indicator (left-footer) ──
   ffmpeg auto-applies TPDF dither when down-converting to pcm_s16le from a
   higher-precision source. There's no explicit "dither" toggle in the UI,
   so this flag confirms it'll happen — otherwise the user might assume the
   conversion is a raw truncation. Hidden on the Fast tab (no 16-bit there)
   and whenever the format/bit-depth combination won't actually dither. */
function updateDitherIndicator() {
    const ind = $('dither-indicator');
    if (!ind) return;
    const activeBtn = /** @type {HTMLElement|null} */ (document.querySelector('.tab-btn.active'));
    const activeTab = (activeBtn && activeBtn.dataset.tab) || 'fast';
    const fmt = $select('adv-format');
    const bd = $select('adv-bd');
    const isPCM = formatKind(fmt ? fmt.value : '') === 'pcm';
    const is16 = (bd ? bd.value : '').startsWith('16');
    const show = activeTab !== 'fast' && isPCM && is16;
    ind.classList.toggle('hidden', !show);
    if (show) ind.textContent = 'Audio will be dithered (TPDF) — 16-bit PCM output';
}

/* ── Processing bypass dim ── */
/** @param {boolean} on */
function updateBypass(on) {
    ['proc-row-dyn', 'proc-row-eq', 'proc-row-dn'].forEach((id) => {
        const el = $(id);
        if (el) el.classList.toggle('dimmed', on);
    });
    const dyn = $select('proc-dyn');
    const eq = $select('proc-eq');
    const dn = $input('proc-dn');
    if (dyn) dyn.disabled = on;
    if (eq) eq.disabled = on;
    if (dn) dn.disabled = on;
    updateChainIndicator();
}

/* ── Status log bar ── */
/** @param {string} text @param {'ok'|'info'|'err'} [type] */
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

/** @param {HTMLElement} el */
function expireLog(el) {
    if (!el.isConnected) return;

    el.style.width = el.getBoundingClientRect().width + 'px';
    el.style.flexShrink = '0';
    void el.offsetWidth;

    el.style.transition = 'opacity 0.3s ease';
    el.style.opacity = '0';

    setTimeout(() => {
        el.style.transition = 'width 0.25s ease, padding-right 0.25s ease';
        el.style.width = '0';
        el.style.paddingRight = '0';

        setTimeout(() => {
            el.remove();
            const bar = $('log-bar');
            if (bar && !bar.querySelector('.log-entry')) {
                const r = document.createElement('span');
                r.className = 'log-ready';
                r.textContent = 'Ready';
                bar.appendChild(r);
            }
        }, 260);
    }, 320);
}

/* ── Progress bar ── */
/** @param {number} fraction Value in 0..1 */
function setProgress(fraction) {
    const track = $('progress-track');
    if (!track) return;
    track.classList.add('visible');
    const fill = /** @type {HTMLElement|null} */ (track.querySelector('.progress-fill'));
    if (fill) fill.style.width = Math.max(0, Math.min(1, fraction)) * 100 + '%';
}

function clearProgress() {
    const track = $('progress-track');
    if (!track) return;
    track.classList.remove('visible');
    const fill = /** @type {HTMLElement|null} */ (track.querySelector('.progress-fill'));
    if (fill) fill.style.width = '0%';
}