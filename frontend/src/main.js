/**
 * BBS Client for Gen-Z — Frontend
 * Terminal renderer (canvas), keyboard handler, Wails event bridge.
 */

// ═══════════════════════════════════════════
// Terminal Renderer (Canvas 80×25)
// ═══════════════════════════════════════════

const COLS = 80;
const ROWS = 25;
const FONT_VT323 = "'VT323', 'Consolas', 'Courier New', monospace";
const FONT_IBM_VGA = "'IBM VGA', 'Consolas', 'Courier New', monospace";
let currentFont = FONT_IBM_VGA; // Default: IBM VGA
let currentFontLabel = 'IBM VGA';
const FONT_SIZE = 16; // fisso, come nel Python (IBM VGA 16pt)

let canvas, ctx;
let cellW = 0, cellH = 0;
let dpr = 1; // devicePixelRatio per Retina
let cursorOn = true;
let cursorX = 0, cursorY = 0;
let screenData = null;
let connected = false;
let viewingLog = false;
let crtEnabled = false;


function syncCrtOverlays() {
    // Assicura che gli overlay CRT coprano esattamente il canvas
    const overlays = document.querySelectorAll('.crt-scanlines, .crt-glow, .crt-vignette, .crt-rgb');
    overlays.forEach(el => {
        el.style.width = canvas.style.width;
        el.style.height = canvas.style.height;
    });
}

function initCanvas() {
    canvas = document.getElementById('terminal');
    ctx = canvas.getContext('2d', { alpha: false });
    resizeCanvas();
    canvas.focus();

    // Cursore lampeggiante
    setInterval(() => {
        cursorOn = !cursorOn;
        renderCursor();
    }, 530);
}

function resizeCanvas() {
    dpr = window.devicePixelRatio || 1;

    // Misura le dimensioni reali dei caratteri a 1x
    ctx.setTransform(1, 0, 0, 1, 0, 0); // reset transform
    ctx.font = `${FONT_SIZE}px ${currentFont}`;
    const testStr = 'M'.repeat(COLS);
    const measuredW = ctx.measureText(testStr).width;
    cellW = Math.round(measuredW / COLS);
    cellH = FONT_SIZE;

    // Dimensioni CSS (logiche)
    const logicalW = cellW * COLS;
    const logicalH = cellH * ROWS;
    canvas.style.width = logicalW + 'px';
    canvas.style.height = logicalH + 'px';

    // Dimensioni fisiche del canvas (Retina: ×dpr)
    canvas.width = logicalW * dpr;
    canvas.height = logicalH * dpr;

    // Scala il contesto per Retina
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

    // Rendering pixel-perfect
    ctx.imageSmoothingEnabled = false;

    // Clear
    ctx.fillStyle = '#000';
    ctx.fillRect(0, 0, logicalW, logicalH);

    // Ridisegna se ci sono dati
    if (screenData) {
        renderScreen(screenData);
    }

    // Sincronizza overlay CRT
    syncCrtOverlays();
}

// Helper: genera una chiave colore per confronto rapido
function colorKey(r, g, b) {
    return (r << 16) | (g << 8) | b;
}

function renderScreen(data) {
    if (!ctx || !data) return;
    screenData = data;

    // ── PASSO 1: Background ──
    // Disegna background riga per riga, raggruppando celle con stesso colore BG
    for (let y = 0; y < ROWS && y < data.length; y++) {
        const row = data[y];
        let x = 0;
        while (x < COLS && x < row.length) {
            const cell = row[x];
            const bgKey = colorKey(cell.bgR, cell.bgG, cell.bgB);
            let runLen = 1;
            while (x + runLen < COLS && x + runLen < row.length) {
                const next = row[x + runLen];
                if (colorKey(next.bgR, next.bgG, next.bgB) !== bgKey) break;
                runLen++;
            }
            const px = x * cellW;
            const py = y * cellH;
            ctx.fillStyle = `rgb(${cell.bgR},${cell.bgG},${cell.bgB})`;
            ctx.fillRect(px, py, cellW * runLen, cellH);
            x += runLen;
        }
    }

    // ── PASSO 2: Testo ──
    // Ogni carattere posizionato esattamente sulla griglia cellW×cellH
    ctx.textBaseline = 'top';
    let lastFont = '';
    let lastFill = '';
    for (let y = 0; y < ROWS && y < data.length; y++) {
        const row = data[y];
        for (let x = 0; x < COLS && x < row.length; x++) {
            const cell = row[x];
            const ch = cell.ch;
            if (!ch || ch === ' ' || ch === '\u0000') continue;

            const px = x * cellW;
            const py = y * cellH;

            // Cambia font solo se necessario
            const font = `${cell.bold ? 'bold ' : ''}${FONT_SIZE}px ${currentFont}`;
            if (font !== lastFont) { ctx.font = font; lastFont = font; }

            // Cambia colore solo se necessario
            const fill = `rgb(${cell.fgR},${cell.fgG},${cell.fgB})`;
            if (fill !== lastFill) { ctx.fillStyle = fill; lastFill = fill; }

            ctx.fillText(ch, px, py);

            if (cell.ul) {
                ctx.strokeStyle = fill;
                ctx.lineWidth = 1;
                ctx.beginPath();
                ctx.moveTo(px, py + cellH - 1);
                ctx.lineTo(px + cellW, py + cellH - 1);
                ctx.stroke();
            }
        }
    }

    renderCursor();
}

function renderCursor() {
    if (!ctx || !screenData) return;

    if (cursorY < screenData.length && cursorX < screenData[cursorY].length) {
        const cell = screenData[cursorY][cursorX];
        const px = cursorX * cellW;
        const py = cursorY * cellH;

        // Ridisegna cella
        ctx.fillStyle = `rgb(${cell.bgR},${cell.bgG},${cell.bgB})`;
        ctx.fillRect(px, py, cellW, cellH);
        if (cell.ch && cell.ch !== ' ' && cell.ch !== '\u0000') {
            ctx.fillStyle = `rgb(${cell.fgR},${cell.fgG},${cell.fgB})`;
            ctx.font = `${cell.bold ? 'bold ' : ''}${FONT_SIZE}px ${currentFont}`;
            ctx.textBaseline = 'top';
            ctx.fillText(cell.ch, px, py);
        }

        // Disegna cursore
        if (cursorOn && document.activeElement === canvas) {
            ctx.globalCompositeOperation = 'difference';
            ctx.fillStyle = 'rgba(0, 255, 65, 0.7)';
            ctx.fillRect(px, py, cellW, cellH);
            ctx.globalCompositeOperation = 'source-over';
        }
    }
}

// ═══════════════════════════════════════════
// Screen Update
// ═══════════════════════════════════════════

async function updateScreen() {
    try {
        // BUG-010: singola chiamata IPC invece di GetScreen + GetCursor
        const snap = await window.go.main.App.GetScreenSnapshot();
        cursorX = snap.cursorX;
        cursorY = snap.cursorY;
        renderScreen(snap.cells);
    } catch (e) {
        console.error('updateScreen error:', e);
    }
}

let updatePending = false;
function requestScreenUpdate() {
    if (updatePending) return;
    updatePending = true;
    requestAnimationFrame(() => {
        updatePending = false;
        updateScreen();
    });
}

// ═══════════════════════════════════════════
// Keyboard Handler
// ═══════════════════════════════════════════

function setupKeyboard() {
    canvas.addEventListener('keydown', async (e) => {
        e.preventDefault();
        e.stopPropagation();

        // Log viewer: navigazione con SPAZIO, frecce, ESC
        if (viewingLog) {
            if (e.key === ' ' || e.key === 'ArrowRight' || e.key === 'ArrowDown' || e.key === 'PageDown') {
                await window.go.main.App.LogNextPage();
            } else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp' || e.key === 'PageUp') {
                await window.go.main.App.LogPrevPage();
            } else if (e.key === 'Escape') {
                await window.go.main.App.LogExit();
            }
            return;
        }

        // F1 o Alt+Z → toggle help overlay
        if (e.key === 'F1' || (e.altKey && e.code === 'KeyZ')) {
            toggleHelp();
            return;
        }

        if (!connected) return;

        // Cmd+D (Mac) o Ctrl+D → disconnetti
        if ((e.metaKey || e.ctrlKey) && e.code === 'KeyD') {
            await window.go.main.App.Disconnect();
            return;
        }

        // Ctrl+lettera
        if (e.ctrlKey && e.key.length === 1) {
            await window.go.main.App.SendCtrlKey(e.key);
            return;
        }

        // Tasti speciali
        const specialKeys = [
            'Enter', 'Backspace', 'Tab', 'Escape',
            'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight',
            'Home', 'End', 'PageUp', 'PageDown', 'Insert', 'Delete',
            'F1', 'F2', 'F3', 'F4', 'F5', 'F6',
            'F7', 'F8', 'F9', 'F10', 'F11', 'F12',
        ];
        if (specialKeys.includes(e.key)) {
            await window.go.main.App.SendSpecialKey(e.key);
            return;
        }

        // Caratteri stampabili
        if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
            await window.go.main.App.SendText(e.key);
        }
    });

    // Mantieni focus sul canvas
    canvas.addEventListener('click', () => canvas.focus());
}

// ═══════════════════════════════════════════
// UI Controls
// ═══════════════════════════════════════════

function setupControls() {
    const btnConnect = document.getElementById('btn-connect');
    const btnHangup = document.getElementById('btn-hangup');
    const btnLog = document.getElementById('btn-log');
    const btnFont = document.getElementById('btn-font');
    const btnClear = document.getElementById('btn-clear');
    const btnUpload = document.getElementById('btn-upload');
    const btnAbout = document.getElementById('btn-about');
    const btnAboutClose = document.getElementById('btn-about-close');
    const hostInput = document.getElementById('host-input');
    const portInput = document.getElementById('port-input');
    const bbsSelect = document.getElementById('bbs-select');

    // Connetti
    btnConnect.addEventListener('click', async () => {
        const host = hostInput.value.trim() || 'bbs.olografix.org';
        const port = parseInt(portInput.value) || 23;
        const bbsName = bbsSelect.options[bbsSelect.selectedIndex]?.text || host;
        btnConnect.disabled = true;
        hostInput.disabled = true;
        portInput.disabled = true;
        bbsSelect.disabled = true;

        const err = await window.go.main.App.Connect(host, port, bbsName);
        if (err) {
            setStatus('Errore: ' + err);
            btnConnect.disabled = false;
            hostInput.disabled = false;
            portInput.disabled = false;
            bbsSelect.disabled = false;
        }
        canvas.focus();
    });

    // Enter nell'input host → connetti
    hostInput.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') btnConnect.click();
    });

    // Disconnect
    btnHangup.addEventListener('click', async () => {
        await window.go.main.App.Disconnect();
    });

    // LOG — carica log sessione
    btnLog.addEventListener('click', async () => {
        const err = await window.go.main.App.LoadLog();
        if (err) {
            setStatus('Errore log: ' + err);
        }
        canvas.focus();
    });

    // FONT — toggle IBM VGA / VT323
    btnFont.addEventListener('click', () => {
        if (currentFont === FONT_IBM_VGA) {
            currentFont = FONT_VT323;
            currentFontLabel = 'VT323';
        } else {
            currentFont = FONT_IBM_VGA;
            currentFontLabel = 'IBM VGA';
        }
        btnFont.textContent = currentFontLabel;
        resizeCanvas();
        setStatus(`Font: ${currentFontLabel}`);
        canvas.focus();
    });
    btnFont.textContent = currentFontLabel;

    // CRT toggle
    const btnCrt = document.getElementById('btn-crt');
    btnCrt.addEventListener('click', () => {
        crtEnabled = !crtEnabled;
        document.body.classList.toggle('crt-on', crtEnabled);
        btnCrt.classList.toggle('active', crtEnabled);
        setStatus(crtEnabled ? 'CRT shader: ON' : 'CRT shader: OFF');
        // Risincronizza dimensioni overlay con canvas
        syncCrtOverlays();
        canvas.focus();
    });

    // PULISCI
    btnClear.addEventListener('click', async () => {
        await window.go.main.App.ClearScreen();
        canvas.focus();
    });

    // UPLOAD — file dialog + ZMODEM
    btnUpload.addEventListener('click', async () => {
        const err = await window.go.main.App.UploadFile();
        if (err) {
            setStatus('Upload: ' + err);
        }
        canvas.focus();
    });

    // About
    btnAbout.addEventListener('click', () => {
        document.getElementById('about-overlay').classList.remove('hidden');
    });
    btnAboutClose.addEventListener('click', () => {
        document.getElementById('about-overlay').classList.add('hidden');
        canvas.focus();
    });

    // ZMODEM cancel
    document.getElementById('btn-zmodem-cancel').addEventListener('click', async () => {
        // Annulla il trasferimento ZMODEM sul backend (se in corso)
        try {
            await window.go.main.App.CancelZmodem();
        } catch (e) {
            console.warn('CancelZmodem:', e);
        }
        document.getElementById('zmodem-overlay').classList.add('hidden');
    });

    // BBS dropdown → aggiorna host/port
    bbsSelect.addEventListener('change', () => {
        const idx = bbsSelect.selectedIndex;
        if (idx >= 0 && bbsList[idx]) {
            hostInput.value = bbsList[idx].host;
            portInput.value = bbsList[idx].port;
        }
    });
}

function setUIConnected(state) {
    const btnConnect = document.getElementById('btn-connect');
    const btnHangup = document.getElementById('btn-hangup');
    const btnUpload = document.getElementById('btn-upload');
    const hostInput = document.getElementById('host-input');
    const portInput = document.getElementById('port-input');
    const bbsSelect = document.getElementById('bbs-select');

    if (state === 'connected') {
        connected = true;
        btnConnect.disabled = true;
        btnHangup.disabled = false;
        btnUpload.disabled = false;
        hostInput.disabled = true;
        portInput.disabled = true;
        bbsSelect.disabled = true;
        const name = bbsSelect.options[bbsSelect.selectedIndex]?.text || '';
        setStatus(`ANSI │ Telnet │ ${name} (${hostInput.value}:${portInput.value}) │ Online`);
    } else {
        connected = false;
        btnConnect.disabled = false;
        btnHangup.disabled = true;
        btnUpload.disabled = true;
        hostInput.disabled = false;
        portInput.disabled = false;
        bbsSelect.disabled = false;
        setStatus('ANSI │ Telnet │ Offline');
    }
}

function setStatus(text) {
    document.getElementById('status-text').textContent = text;
}

// ═══════════════════════════════════════════
// Help Overlay (Alt-Z)
// ═══════════════════════════════════════════

function toggleHelp() {
    const overlay = document.getElementById('help-overlay');
    overlay.classList.toggle('hidden');
    if (overlay.classList.contains('hidden')) {
        canvas.focus();
    }
}

function setupHelp() {
    document.getElementById('btn-help-close').addEventListener('click', () => {
        toggleHelp();
    });

    // ESC chiude l'help se è aperto
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape' && !document.getElementById('help-overlay').classList.contains('hidden')) {
            e.preventDefault();
            e.stopPropagation();
            toggleHelp();
        }
    });
}

// ═══════════════════════════════════════════
// ZMODEM Progress UI
// ═══════════════════════════════════════════

function showZmodemProgress(filename, filesize) {
    const overlay = document.getElementById('zmodem-overlay');
    document.getElementById('zmodem-title').textContent = 'ZMODEM Download';
    document.getElementById('zmodem-title').style.color = '#FFFF55';
    document.getElementById('zmodem-file').textContent = 'File: ' + filename;
    document.getElementById('zmodem-bytes').textContent = `0 / ${formatBytes(filesize)}`;
    document.getElementById('zmodem-speed').textContent = 'Velocità: — KB/s';
    document.getElementById('zmodem-eta').textContent = 'ETA: —';
    document.getElementById('zmodem-bar').style.width = '0%';
    document.getElementById('btn-zmodem-cancel').textContent = 'ANNULLA';
    overlay.classList.remove('hidden');
}

function updateZmodemProgress(bytes, total, speed) {
    const pct = total > 0 ? Math.round(bytes * 100 / total) : 0;
    document.getElementById('zmodem-bar').style.width = pct + '%';
    document.getElementById('zmodem-bytes').textContent =
        `${formatBytes(bytes)} / ${formatBytes(total)} (${pct}%)`;
    document.getElementById('zmodem-speed').textContent =
        `Velocità: ${speed.toFixed(1)} KB/s`;

    if (speed > 0 && total > bytes) {
        const remaining = (total - bytes) / 1024 / speed;
        const min = Math.floor(remaining / 60);
        const sec = Math.floor(remaining % 60);
        document.getElementById('zmodem-eta').textContent =
            `ETA: ${String(min).padStart(2, '0')}:${String(sec).padStart(2, '0')}`;
    }
}

function showZmodemComplete(filepath) {
    document.getElementById('zmodem-title').textContent = 'Download completato!';
    document.getElementById('zmodem-title').style.color = '#55FF55';
    document.getElementById('zmodem-eta').textContent = 'Completato';
    document.getElementById('zmodem-bar').style.width = '100%';
    document.getElementById('btn-zmodem-cancel').textContent = 'CHIUDI';
}

function showZmodemError(message) {
    document.getElementById('zmodem-title').textContent = 'Errore ZMODEM';
    document.getElementById('zmodem-title').style.color = '#FF5555';
    document.getElementById('zmodem-eta').textContent = message;
    document.getElementById('btn-zmodem-cancel').textContent = 'CHIUDI';
}

function formatBytes(b) {
    if (b > 1024 * 1024) return (b / 1024 / 1024).toFixed(1) + ' MB';
    if (b > 1024) return (b / 1024).toFixed(1) + ' KB';
    return b + ' bytes';
}

// ═══════════════════════════════════════════
// Wails Events
// ═══════════════════════════════════════════

let bbsList = [];

function setupEvents() {
    // Screen update dal backend
    window.runtime.EventsOn('screen-update', () => {
        requestScreenUpdate();
    });

    // Connection status
    window.runtime.EventsOn('connection-status', (status) => {
        setUIConnected(status);
        if (status === 'connected') {
            canvas.focus();
        }
    });

    // Status message
    window.runtime.EventsOn('status-message', (msg) => {
        setStatus(msg);
    });

    // Log mode
    window.runtime.EventsOn('log-mode', (data) => {
        if (data === false) {
            viewingLog = false;
            setStatus('ANSI │ Telnet │ Offline');
        } else if (data && data.active) {
            viewingLog = true;
            setStatus(`Log [${data.page}/${data.total}] — SPAZIO avanti, ← indietro, ESC esci`);
        }
    });

    // ZMODEM events
    window.runtime.EventsOn('zmodem-started', (data) => {
        showZmodemProgress(data.filename, data.filesize);
    });
    window.runtime.EventsOn('zmodem-progress', (data) => {
        updateZmodemProgress(data.bytes, data.total, data.speed);
    });
    window.runtime.EventsOn('zmodem-finished', (data) => {
        showZmodemComplete(data.filepath);
    });
    window.runtime.EventsOn('zmodem-error', (msg) => {
        showZmodemError(msg);
    });
}

// ═══════════════════════════════════════════
// BBS List
// ═══════════════════════════════════════════

async function loadBBSList() {
    try {
        bbsList = await window.go.main.App.GetBBSList();
        const select = document.getElementById('bbs-select');
        select.innerHTML = '';
        let defaultIdx = 0;
        bbsList.forEach((entry, i) => {
            const opt = document.createElement('option');
            opt.textContent = entry.name;
            opt.value = i;
            select.appendChild(opt);
            // Cerca Metro Olografix come default
            if (entry.host === 'bbs.olografix.org' || entry.name.toLowerCase().includes('olografix')) {
                defaultIdx = i;
            }
        });
        select.selectedIndex = defaultIdx;
        // Imposta host/port dall'elemento selezionato
        if (bbsList.length > 0) {
            document.getElementById('host-input').value = bbsList[defaultIdx].host;
            document.getElementById('port-input').value = bbsList[defaultIdx].port;
        }
    } catch (e) {
        console.error('loadBBSList error:', e);
    }
}

// ═══════════════════════════════════════════
// Init
// ═══════════════════════════════════════════

document.addEventListener('DOMContentLoaded', async () => {
    // Aspetta che il font IBM VGA sia caricato prima di misurare le celle
    try {
        await document.fonts.load(`${FONT_SIZE}px "IBM VGA"`);
        await document.fonts.ready;
    } catch (e) {
        console.warn('Font preload warning:', e);
    }

    initCanvas();
    setupKeyboard();
    setupControls();
    setupHelp();

    // Aspetta che Wails sia pronto
    await new Promise(resolve => {
        if (window.runtime) {
            resolve();
        } else {
            const check = setInterval(() => {
                if (window.runtime) {
                    clearInterval(check);
                    resolve();
                }
            }, 50);
        }
    });

    setupEvents();
    await loadBBSList();

    // Messaggio iniziale
    ctx.fillStyle = '#000';
    ctx.fillRect(0, 0, canvas.width / dpr, canvas.height / dpr);
    ctx.font = `${FONT_SIZE}px ${currentFont}`;
    ctx.fillStyle = '#FFFF55';
    ctx.fillText('BBS Client for Gen-Z v1.1.0 — Pronto', 10, 20);
    ctx.fillStyle = '#55FFFF';
    ctx.fillText('Seleziona una BBS e premi CONNETTI', 10, 44);
    ctx.fillStyle = '#555555';
    ctx.fillText('F1 = Help  │  Cmd+D = Disconnetti', 10, 68);

    canvas.focus();
});
