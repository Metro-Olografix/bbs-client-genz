package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/rj45lab/bbs-client-go/internal/ansi"
	"github.com/rj45lab/bbs-client-go/internal/telnet"
)

//go:embed short_*.txt
var bbsListFS embed.FS

// ─────────────────────────────────────────────
// ScreenCell — cella esportata al frontend (JSON)
// ─────────────────────────────────────────────

type ScreenCell struct {
	Char      string `json:"ch"`
	FgR       uint8  `json:"fgR"`
	FgG       uint8  `json:"fgG"`
	FgB       uint8  `json:"fgB"`
	BgR       uint8  `json:"bgR"`
	BgG       uint8  `json:"bgG"`
	BgB       uint8  `json:"bgB"`
	Bold      bool   `json:"bold"`
	Underline bool   `json:"ul"`
	Blink     bool   `json:"blink"`
	Reverse   bool   `json:"rev"`
}

// ScreenSnapshot — schermo + cursore in una singola risposta (BUG-010)
type ScreenSnapshot struct {
	Cells   [][]ScreenCell `json:"cells"`
	CursorX int            `json:"cursorX"`
	CursorY int            `json:"cursorY"`
}

// ─────────────────────────────────────────────
// BBS Entry per il dropdown
// ─────────────────────────────────────────────

type BBSEntry struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

// ─────────────────────────────────────────────
// App — struct principale Wails
// ─────────────────────────────────────────────

type App struct {
	ctx    context.Context
	conn   *telnet.Connection
	screen *ansi.Screen
	mu     sync.Mutex

	// Stato
	host      string
	port      int
	connected bool

	// BBS list
	bbsList []BBSEntry

	// Log viewer
	logPages   []string
	logPageIdx int
	viewingLog bool

	// Session logger
	logFile *os.File
	logDir  string
}

// NewApp crea l'app.
func NewApp() *App {
	return &App{
		host: telnet.DefaultHost,
		port: telnet.DefaultPort,
	}
}

// Startup è chiamato da Wails all'avvio.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.screen = ansi.NewScreen(80, 25)
	a.conn = telnet.New()
	a.conn.SetDownloadDir(a.downloadDir())

	// DSR callback
	a.screen.OnResponse = func(data []byte) {
		a.conn.Send(data)
	}

	// Prepara directory logs (SEC-005: 0700 per proteggere dati sensibili)
	a.logDir = a.logsDir()
	os.MkdirAll(a.logDir, 0700)

	// Carica lista BBS
	a.bbsList = a.loadBBSList()

	// Goroutine per gestire eventi dalla connessione telnet
	go a.eventLoop()
}

func (a *App) downloadDir() string {
	exe, _ := os.Executable()
	return filepath.Join(filepath.Dir(exe), "downloads")
}

func (a *App) logsDir() string {
	exe, _ := os.Executable()
	return filepath.Join(filepath.Dir(exe), "logs")
}

// startSessionLog apre un nuovo file di log per la sessione corrente.
func (a *App) startSessionLog(bbsName, host string, port int) {
	a.stopSessionLog() // chiudi eventuale log precedente

	// Sanitizza il nome BBS per il filename
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, bbsName)
	if safe == "" {
		safe = host
	}

	ts := time.Now().Format("2006-01-02_150405")
	filename := fmt.Sprintf("%s_%s.log", safe, ts)
	path := filepath.Join(a.logDir, filename)

	f, err := os.Create(path)
	if err != nil {
		return
	}
	a.logFile = f
	logBytesWritten = 0 // PT-004: reset contatore

	// Intestazione
	header := fmt.Sprintf("=== Sessione %s (%s:%d) — %s ===\n",
		bbsName, host, port, time.Now().Format("2006-01-02 15:04:05"))
	f.WriteString(header)
}

// maxLogSize è il limite massimo per file di log (PT-004: anti-flooding)
const maxLogSize = 50 * 1024 * 1024 // 50 MB

// logBytesWritten conta i byte scritti nel log corrente
var logBytesWritten int64

// writeSessionLog scrive dati decodificati (con sequenze ANSI) nel log.
func (a *App) writeSessionLog(text string) {
	if a.logFile != nil {
		// PT-004: limita dimensione log per prevenire DoS locale
		if logBytesWritten > maxLogSize {
			return // silenziosamente ignora dopo il limite
		}
		n, _ := a.logFile.WriteString(text)
		logBytesWritten += int64(n)
	}
}

// stopSessionLog chiude il file di log corrente.
func (a *App) stopSessionLog() {
	if a.logFile != nil {
		footer := fmt.Sprintf("\n=== Fine sessione — %s ===\n",
			time.Now().Format("2006-01-02 15:04:05"))
		a.logFile.WriteString(footer)
		a.logFile.Close()
		a.logFile = nil
	}
}

// ─────────────────────────────────────────────
// Metodi esposti al frontend (Wails bindings)
// ─────────────────────────────────────────────

// Connect si connette alla BBS. bbsName è il nome visualizzato nel dropdown.
func (a *App) Connect(host string, port int, bbsName string) string {
	a.mu.Lock()
	if a.connected {
		a.mu.Unlock()
		return "Già connesso"
	}
	a.mu.Unlock()
	if host == "" {
		host = telnet.DefaultHost
	}
	if port <= 0 {
		port = telnet.DefaultPort
	}
	a.host = host
	a.port = port

	// Avvia session log
	if bbsName == "" {
		bbsName = host
	}
	a.startSessionLog(bbsName, host, port)

	// BUG-007: reset screen prima di nuova connessione
	a.mu.Lock()
	a.screen.Reset()
	a.mu.Unlock()
	wailsrt.EventsEmit(a.ctx, "screen-update", true)

	err := a.conn.Connect(host, port)
	if err != nil {
		a.stopSessionLog()
		return fmt.Sprintf("Errore: %v", err)
	}
	return ""
}

// Disconnect chiude la connessione.
func (a *App) Disconnect() {
	a.conn.Disconnect()
	a.mu.Lock()
	a.connected = false
	a.mu.Unlock()
	a.stopSessionLog()
	wailsrt.EventsEmit(a.ctx, "connection-status", "disconnected")
}

// SendKey invia un tasto al server (chiamato dal frontend su keydown).
func (a *App) SendKey(data []byte) {
	a.mu.Lock()
	ok := a.connected
	a.mu.Unlock()
	if ok {
		a.conn.Send(data)
	}
}

// SendText invia una stringa come bytes CP437 al server.
func (a *App) SendText(text string) {
	a.mu.Lock()
	ok := a.connected
	a.mu.Unlock()
	if !ok {
		return
	}
	// Converti da UTF-8 a bytes da inviare
	a.conn.Send([]byte(text))
}

// SendSpecialKey invia un tasto speciale (arrow, F-key, ecc.)
func (a *App) SendSpecialKey(key string) {
	a.mu.Lock()
	ok := a.connected
	a.mu.Unlock()
	if !ok {
		return
	}
	keyMap := map[string][]byte{
		"Enter":     {0x0D},
		"Backspace": {0x08},
		"Tab":       {0x09},
		"Escape":    {0x1B},
		"ArrowUp":   {0x1B, '[', 'A'},
		"ArrowDown": {0x1B, '[', 'B'},
		"ArrowRight":{0x1B, '[', 'C'},
		"ArrowLeft": {0x1B, '[', 'D'},
		"Home":      {0x1B, '[', 'H'},
		"End":       {0x1B, '[', 'F'},
		"PageUp":    {0x1B, '[', '5', '~'},
		"PageDown":  {0x1B, '[', '6', '~'},
		"Insert":    {0x1B, '[', '2', '~'},
		"Delete":    {0x1B, '[', '3', '~'},
		"F1":        {0x1B, 'O', 'P'},
		"F2":        {0x1B, 'O', 'Q'},
		"F3":        {0x1B, 'O', 'R'},
		"F4":        {0x1B, 'O', 'S'},
		"F5":        {0x1B, '[', '1', '5', '~'},
		"F6":        {0x1B, '[', '1', '7', '~'},
		"F7":        {0x1B, '[', '1', '8', '~'},
		"F8":        {0x1B, '[', '1', '9', '~'},
		"F9":        {0x1B, '[', '2', '0', '~'},
		"F10":       {0x1B, '[', '2', '1', '~'},
		"F11":       {0x1B, '[', '2', '3', '~'},
		"F12":       {0x1B, '[', '2', '4', '~'},
	}
	if data, ok := keyMap[key]; ok {
		a.conn.Send(data)
	}
}

// SendCtrlKey invia Ctrl+lettera
func (a *App) SendCtrlKey(letter string) {
	a.mu.Lock()
	ok := a.connected
	a.mu.Unlock()
	if !ok || len(letter) == 0 {
		return
	}
	ch := letter[0]
	if ch >= 'a' && ch <= 'z' {
		ch -= 'a' - 'A'
	}
	if ch >= 'A' && ch <= 'Z' {
		a.conn.Send([]byte{ch - 0x40})
	}
}

// GetScreen ritorna lo stato attuale dello schermo come array 2D di celle.
func (a *App) GetScreen() [][]ScreenCell {
	a.mu.Lock()
	defer a.mu.Unlock()

	rows := make([][]ScreenCell, a.screen.Rows)
	for y := 0; y < a.screen.Rows; y++ {
		row := make([]ScreenCell, a.screen.Cols)
		for x := 0; x < a.screen.Cols; x++ {
			cell := a.screen.Buffer[y][x]
			fgR, fgG, fgB := cell.Attr.FG.ToRGB(true, cell.Attr.Bold)
			bgR, bgG, bgB := cell.Attr.BG.ToRGB(false, false)
			if cell.Attr.Reverse {
				fgR, fgG, fgB, bgR, bgG, bgB = bgR, bgG, bgB, fgR, fgG, fgB
			}
			ch := string(cell.Char)
			if cell.Char < 0x20 {
				ch = " "
			}
			row[x] = ScreenCell{
				Char: ch,
				FgR: fgR, FgG: fgG, FgB: fgB,
				BgR: bgR, BgG: bgG, BgB: bgB,
				Bold: cell.Attr.Bold, Underline: cell.Attr.Underline,
				Blink: cell.Attr.Blink, Reverse: cell.Attr.Reverse,
			}
		}
		rows[y] = row
	}
	return rows
}

// GetCursor ritorna posizione cursore {x, y}.
func (a *App) GetCursor() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]int{"x": a.screen.CursorX, "y": a.screen.CursorY}
}

// GetScreenSnapshot ritorna schermo + cursore in una singola chiamata IPC (BUG-010).
func (a *App) GetScreenSnapshot() ScreenSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()

	rows := make([][]ScreenCell, a.screen.Rows)
	for y := 0; y < a.screen.Rows; y++ {
		row := make([]ScreenCell, a.screen.Cols)
		for x := 0; x < a.screen.Cols; x++ {
			cell := a.screen.Buffer[y][x]
			fgR, fgG, fgB := cell.Attr.FG.ToRGB(true, cell.Attr.Bold)
			bgR, bgG, bgB := cell.Attr.BG.ToRGB(false, false)
			if cell.Attr.Reverse {
				fgR, fgG, fgB, bgR, bgG, bgB = bgR, bgG, bgB, fgR, fgG, fgB
			}
			ch := string(cell.Char)
			if cell.Char < 0x20 {
				ch = " "
			}
			row[x] = ScreenCell{
				Char: ch,
				FgR: fgR, FgG: fgG, FgB: fgB,
				BgR: bgR, BgG: bgG, BgB: bgB,
				Bold: cell.Attr.Bold, Underline: cell.Attr.Underline,
				Blink: cell.Attr.Blink, Reverse: cell.Attr.Reverse,
			}
		}
		rows[y] = row
	}
	return ScreenSnapshot{
		Cells:   rows,
		CursorX: a.screen.CursorX,
		CursorY: a.screen.CursorY,
	}
}

// GetBBSList ritorna la lista delle BBS disponibili.
func (a *App) GetBBSList() []BBSEntry {
	return a.bbsList
}

// ClearScreen pulisce lo schermo.
func (a *App) ClearScreen() {
	a.mu.Lock()
	a.screen.Reset()
	a.mu.Unlock()
	wailsrt.EventsEmit(a.ctx, "screen-update", true)
}

// IsConnected ritorna lo stato di connessione.
func (a *App) IsConnected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.connected
}

// UploadFile apre un file dialog e avvia upload ZMODEM.
func (a *App) UploadFile() string {
	a.mu.Lock()
	ok := a.connected
	a.mu.Unlock()
	if !ok {
		return "Non connesso"
	}
	path, err := wailsrt.OpenFileDialog(a.ctx, wailsrt.OpenDialogOptions{
		Title: "Seleziona file per upload ZMODEM",
	})
	if err != nil {
		return fmt.Sprintf("Errore: %v", err)
	}
	if path == "" {
		return "" // annullato
	}
	go func() {
		a.conn.StartZmodemUpload(path)
	}()
	return ""
}

// CancelZmodem annulla il trasferimento ZMODEM in corso.
func (a *App) CancelZmodem() {
	a.conn.CancelZmodem()
}

// LoadLog apre un file di log sessione e lo renderizza nel terminale.
func (a *App) LoadLog() string {
	path, err := wailsrt.OpenFileDialog(a.ctx, wailsrt.OpenDialogOptions{
		Title:            "Apri log sessione",
		DefaultDirectory: a.logDir,
		Filters: []wailsrt.FileFilter{
			{DisplayName: "Log files (*.log)", Pattern: "*.log"},
			{DisplayName: "Tutti i file (*)", Pattern: "*"},
		},
	})
	if err != nil {
		return fmt.Sprintf("Errore: %v", err)
	}
	if path == "" {
		return "" // annullato
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("Errore lettura: %v", err)
	}

	// Se connesso, disconnetti
	a.mu.Lock()
	wasConn := a.connected
	if wasConn {
		a.connected = false
	}
	a.mu.Unlock()
	if wasConn {
		a.conn.Disconnect()
	}

	// Rimuovi intestazione/chiusura sessione
	text := string(content)
	text = regexp.MustCompile(`(?m)^=== Sessione .+===\n?`).ReplaceAllString(text, "")
	text = regexp.MustCompile(`\n?=== Fine sessione .+===$`).ReplaceAllString(text, "")

	// Splitta in pagine su ESC[2J (clear screen)
	clearSeq := "\x1b[2J"
	parts := strings.Split(text, clearSeq)
	var cleanPages []string
	for i, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		// Riaggiungi ESC[2J all'inizio di ogni parte tranne la prima
		if i > 0 {
			p = clearSeq + p
		}
		cleanPages = append(cleanPages, p)
	}
	if len(cleanPages) == 0 {
		cleanPages = []string{text}
	}

	// Salva le pagine per navigazione
	a.mu.Lock()
	a.logPages = cleanPages
	a.logPageIdx = 0
	a.viewingLog = true
	a.mu.Unlock()

	a.showLogPage()
	return ""
}

// LogNextPage avanza alla pagina successiva del log.
func (a *App) LogNextPage() {
	a.mu.Lock()
	if a.logPageIdx < len(a.logPages)-1 {
		a.logPageIdx++
	}
	a.mu.Unlock()
	a.showLogPage()
}

// LogPrevPage torna alla pagina precedente.
func (a *App) LogPrevPage() {
	a.mu.Lock()
	if a.logPageIdx > 0 {
		a.logPageIdx--
	}
	a.mu.Unlock()
	a.showLogPage()
}

// LogExit esce dalla visualizzazione log.
func (a *App) LogExit() {
	a.mu.Lock()
	a.viewingLog = false
	a.logPages = nil
	a.logPageIdx = 0
	a.screen.Reset()
	a.mu.Unlock()
	wailsrt.EventsEmit(a.ctx, "log-mode", false)
	wailsrt.EventsEmit(a.ctx, "screen-update", true)
}

// IsViewingLog ritorna se siamo in modalità log.
func (a *App) IsViewingLog() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.viewingLog
}

func (a *App) showLogPage() {
	a.mu.Lock()
	if len(a.logPages) == 0 {
		a.mu.Unlock()
		return
	}
	page := a.logPages[a.logPageIdx]
	current := a.logPageIdx + 1
	total := len(a.logPages)
	a.screen.Reset()
	a.screen.Feed(page)

	// Barra navigazione in ultima riga (reverse video)
	var hint string
	if current < total {
		hint = "SPAZIO ▶ avanti  |  ← indietro  |  ESC ✖ esci"
	} else {
		hint = "ULTIMA PAGINA  |  ← indietro  |  ESC ✖ esci"
	}
	bar := fmt.Sprintf(" Log [%d/%d]  %s ", current, total, hint)
	// BUG-006: pad a 80 colonne usando conteggio rune (non byte) per Unicode
	for utf8.RuneCountInString(bar) < 80 {
		bar += " "
	}
	prompt := fmt.Sprintf("\x1b[25;1H\x1b[0;7m%s\x1b[0m", bar)
	a.screen.Feed(prompt)
	a.mu.Unlock()

	wailsrt.EventsEmit(a.ctx, "log-mode", map[string]interface{}{
		"active": true, "page": current, "total": total,
	})
	wailsrt.EventsEmit(a.ctx, "screen-update", true)
}

// ─────────────────────────────────────────────
// Event loop — bridge tra telnet events e Wails frontend
// ─────────────────────────────────────────────

func (a *App) eventLoop() {
	for {
		select {
		case <-a.ctx.Done():
			// BUG-002: termina la goroutine quando l'app si chiude
			return

		case data := <-a.conn.DataCh:
			// Decodifica CP437 e alimenta lo screen buffer
			text := decodeCp437(data)
			a.mu.Lock()
			a.screen.Feed(text)
			a.mu.Unlock()
			// Scrivi nel log sessione (con sequenze ANSI intatte)
			a.writeSessionLog(text)
			// Notifica il frontend di aggiornare lo schermo
			wailsrt.EventsEmit(a.ctx, "screen-update", true)

		case event := <-a.conn.EventCh:
			switch event.Type {
			case telnet.EventConnected:
				a.mu.Lock()
				a.connected = true
				a.mu.Unlock()
				wailsrt.EventsEmit(a.ctx, "connection-status", "connected")
			case telnet.EventDisconnected:
				a.mu.Lock()
				a.connected = false
				a.mu.Unlock()
				a.stopSessionLog()
				wailsrt.EventsEmit(a.ctx, "connection-status", "disconnected")
				wailsrt.EventsEmit(a.ctx, "status-message", "Disconnesso: "+event.Message)
			case telnet.EventError:
				a.mu.Lock()
				a.connected = false
				a.mu.Unlock()
				a.stopSessionLog()
				wailsrt.EventsEmit(a.ctx, "connection-status", "error")
				wailsrt.EventsEmit(a.ctx, "status-message", "Errore: "+event.Message)
			case telnet.EventZmodemStarted:
				wailsrt.EventsEmit(a.ctx, "zmodem-started", map[string]interface{}{
					"filename": event.Filename, "filesize": event.Filesize,
				})
			case telnet.EventZmodemProgress:
				wailsrt.EventsEmit(a.ctx, "zmodem-progress", map[string]interface{}{
					"bytes": event.Bytes, "total": event.Filesize, "speed": event.Speed,
				})
			case telnet.EventZmodemFinished:
				wailsrt.EventsEmit(a.ctx, "zmodem-finished", map[string]interface{}{
					"filepath": event.Filepath, "success": event.Success,
				})
			case telnet.EventZmodemError:
				wailsrt.EventsEmit(a.ctx, "zmodem-error", event.Message)
			}
		}
	}
}

// ─────────────────────────────────────────────
// Caricamento lista BBS
// ─────────────────────────────────────────────

func (a *App) loadBBSList() []BBSEntry {
	fallback := []BBSEntry{
		{Name: "Metro Olografix", Host: "bbs.olografix.org", Port: 23},
	}

	// 1. Prima prova dal filesystem (file esterni aggiornabili)
	content := a.loadBBSFromDisk()

	// 2. Se non trovato, usa il file embeddato nella build
	if content == "" {
		content = a.loadBBSFromEmbed()
	}

	if content == "" {
		return fallback
	}

	parsed := parseBBSList(content)
	if len(parsed) > 0 {
		return parsed
	}
	return fallback
}

func (a *App) loadBBSFromDisk() string {
	// Cerca vicino all'eseguibile
	exe, err := os.Executable()
	if err == nil {
		baseDir := filepath.Dir(exe)
		if s := findLatestShortFile(baseDir); s != "" {
			return s
		}
	}
	// Prova nella directory corrente
	if s := findLatestShortFile("."); s != "" {
		return s
	}
	return ""
}

func findLatestShortFile(dir string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "short_*.txt"))
	if len(matches) == 0 {
		return ""
	}
	var latest string
	var latestTime time.Time
	for _, m := range matches {
		info, err := os.Stat(m)
		if err == nil && info.ModTime().After(latestTime) {
			latest = m
			latestTime = info.ModTime()
		}
	}
	data, err := os.ReadFile(latest)
	if err != nil {
		return ""
	}
	return string(data)
}

func (a *App) loadBBSFromEmbed() string {
	entries, err := bbsListFS.ReadDir(".")
	if err != nil {
		return ""
	}
	// Trova l'ultimo file short_*.txt embeddato
	var latest string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "short_") && strings.HasSuffix(e.Name(), ".txt") {
			if e.Name() > latest {
				latest = e.Name()
			}
		}
	}
	if latest == "" {
		return ""
	}
	data, err := bbsListFS.ReadFile(latest)
	if err != nil {
		return ""
	}
	return string(data)
}

func parseBBSList(content string) []BBSEntry {
	lines := strings.Split(content, "\n")
	start := 0
	for i, line := range lines {
		if strings.Contains(line, "* = NEW listing") {
			start = i + 1
			break
		}
	}

	var parsed []BBSEntry
	for _, line := range lines[start:] {
		raw := strings.TrimRight(line, "\r\n")
		stripped := strings.TrimSpace(raw)
		if strings.HasPrefix(stripped, "---") || strings.HasPrefix(stripped, "Added in") ||
			strings.HasPrefix(stripped, "TOTAL") {
			break
		}
		if stripped == "" || strings.HasPrefix(stripped, "===") {
			continue
		}
		parts := splitBySpaces(raw)
		if len(parts) < 2 {
			continue
		}
		name := parts[0]
		addrStr := parts[1]
		host := addrStr
		port := 23
		if idx := strings.LastIndex(addrStr, ":"); idx >= 0 {
			portStr := addrStr[idx+1:]
			host = addrStr[:idx]
			fmt.Sscanf(portStr, "%d", &port)
		}
		if host != "" {
			parsed = append(parsed, BBSEntry{Name: name, Host: host, Port: port})
		}
	}
	return parsed
}

func splitBySpaces(s string) []string {
	s = strings.TrimLeft(s, "* ")
	idx := strings.Index(s, "  ")
	if idx < 0 {
		return nil
	}
	name := strings.TrimSpace(s[:idx])
	addr := strings.TrimSpace(s[idx:])
	if name == "" || addr == "" {
		return nil
	}
	return []string{name, addr}
}

// ─────────────────────────────────────────────
// CP437 decode (stessa tabella del CLI main.go)
// ─────────────────────────────────────────────

var cp437ToUnicode = [256]rune{
	0x0000, 0x263A, 0x263B, 0x2665, 0x2666, 0x2663, 0x2660, 0x2022,
	0x25D8, 0x25CB, 0x25D9, 0x2642, 0x2640, 0x266A, 0x266B, 0x263C,
	0x25BA, 0x25C4, 0x2195, 0x203C, 0x00B6, 0x00A7, 0x25AC, 0x21A8,
	0x2191, 0x2193, 0x2192, 0x2190, 0x221F, 0x2194, 0x25B2, 0x25BC,
	' ', '!', '"', '#', '$', '%', '&', '\'',
	'(', ')', '*', '+', ',', '-', '.', '/',
	'0', '1', '2', '3', '4', '5', '6', '7',
	'8', '9', ':', ';', '<', '=', '>', '?',
	'@', 'A', 'B', 'C', 'D', 'E', 'F', 'G',
	'H', 'I', 'J', 'K', 'L', 'M', 'N', 'O',
	'P', 'Q', 'R', 'S', 'T', 'U', 'V', 'W',
	'X', 'Y', 'Z', '[', '\\', ']', '^', '_',
	'`', 'a', 'b', 'c', 'd', 'e', 'f', 'g',
	'h', 'i', 'j', 'k', 'l', 'm', 'n', 'o',
	'p', 'q', 'r', 's', 't', 'u', 'v', 'w',
	'x', 'y', 'z', '{', '|', '}', '~', 0x2302,
	0x00C7, 0x00FC, 0x00E9, 0x00E2, 0x00E4, 0x00E0, 0x00E5, 0x00E7,
	0x00EA, 0x00EB, 0x00E8, 0x00EF, 0x00EE, 0x00EC, 0x00C4, 0x00C5,
	0x00C9, 0x00E6, 0x00C6, 0x00F4, 0x00F6, 0x00F2, 0x00FB, 0x00F9,
	0x00FF, 0x00D6, 0x00DC, 0x00A2, 0x00A3, 0x00A5, 0x20A7, 0x0192,
	0x00E1, 0x00ED, 0x00F3, 0x00FA, 0x00F1, 0x00D1, 0x00AA, 0x00BA,
	0x00BF, 0x2310, 0x00AC, 0x00BD, 0x00BC, 0x00A1, 0x00AB, 0x00BB,
	0x2591, 0x2592, 0x2593, 0x2502, 0x2524, 0x2561, 0x2562, 0x2556,
	0x2555, 0x2563, 0x2551, 0x2557, 0x255D, 0x255C, 0x255B, 0x2510,
	0x2514, 0x2534, 0x252C, 0x251C, 0x2500, 0x253C, 0x255E, 0x255F,
	0x255A, 0x2554, 0x2569, 0x2566, 0x2560, 0x2550, 0x256C, 0x2567,
	0x2568, 0x2564, 0x2565, 0x2559, 0x2558, 0x2552, 0x2553, 0x256B,
	0x256A, 0x2518, 0x250C, 0x2588, 0x2584, 0x258C, 0x2590, 0x2580,
	0x03B1, 0x00DF, 0x0393, 0x03C0, 0x03A3, 0x03C3, 0x00B5, 0x03C4,
	0x03A6, 0x0398, 0x03A9, 0x03B4, 0x221E, 0x03C6, 0x03B5, 0x2229,
	0x2261, 0x00B1, 0x2265, 0x2264, 0x2320, 0x2321, 0x00F7, 0x2248,
	0x00B0, 0x2219, 0x00B7, 0x221A, 0x207F, 0x00B2, 0x25A0, 0x00A0,
}

func decodeCp437(data []byte) string {
	runes := make([]rune, len(data))
	for i, b := range data {
		if b < 0x20 {
			// Preserva i caratteri di controllo (ESC, CR, LF, BS, TAB, BEL)
			// così il parser ANSI li riconosce correttamente.
			runes[i] = rune(b)
		} else {
			runes[i] = cp437ToUnicode[b]
		}
	}
	return string(runes)
}
