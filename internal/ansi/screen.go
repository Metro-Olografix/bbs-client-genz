// Package ansi implementa un emulatore terminale ANSI per BBS.
//
// Porting da AnsiScreen (bbs_client.py) → Go idiomatico.
// Gestisce SGR (colori 16/256/TrueColor), posizionamento cursore,
// cancellazione schermo, scroll e salvataggio cursore.
package ansi

import (
	"strconv"
	"strings"
)

// ─────────────────────────────────────────────
// Costanti
// ─────────────────────────────────────────────

const (
	DefaultFG = 7 // grigio chiaro
	DefaultBG = 0 // nero
	MaxCSIBuf = 1024
)

// Palette IBM VGA 16 colori (R, G, B)
var Palette16 = [16][3]uint8{
	{0, 0, 0},       //  0  Nero
	{170, 0, 0},     //  1  Rosso
	{0, 170, 0},     //  2  Verde
	{170, 85, 0},    //  3  Marrone / Giallo scuro
	{0, 0, 170},     //  4  Blu
	{170, 0, 170},   //  5  Magenta
	{0, 170, 170},   //  6  Ciano
	{170, 170, 170}, //  7  Grigio chiaro (default fg)
	{85, 85, 85},    //  8  Grigio scuro
	{255, 85, 85},   //  9  Rosso chiaro
	{85, 255, 85},   // 10  Verde chiaro
	{255, 255, 85},  // 11  Giallo
	{85, 85, 255},   // 12  Blu chiaro
	{255, 85, 255},  // 13  Magenta chiaro
	{85, 255, 255},  // 14  Ciano chiaro
	{255, 255, 255}, // 15  Bianco
}

// ─────────────────────────────────────────────
// Color — colore flessibile (indice 0-255 o RGB)
// ─────────────────────────────────────────────

// Color rappresenta un colore che può essere un indice palette (0-255)
// o un colore RGB diretto (TrueColor).
type Color struct {
	Index   int  // 0-255 per palette, -1 se è RGB
	R, G, B uint8
	IsRGB   bool
}

// IndexColor crea un Color da indice palette.
func IndexColor(idx int) Color {
	return Color{Index: idx}
}

// RGBColor crea un Color da valori RGB.
func RGBColor(r, g, b uint8) Color {
	return Color{Index: -1, R: r, G: g, B: b, IsRGB: true}
}

// ToRGB converte qualsiasi Color in valori RGB, applicando bold se fg.
func (c Color) ToRGB(isFG, bold bool) (uint8, uint8, uint8) {
	if c.IsRGB {
		return c.R, c.G, c.B
	}

	idx := c.Index

	// Bold su foreground standard → bright
	if isFG && bold && idx >= 0 && idx <= 7 {
		idx += 8
	}

	// 16 colori standard
	if idx >= 0 && idx <= 15 {
		return Palette16[idx][0], Palette16[idx][1], Palette16[idx][2]
	}

	// 216 colori (cubo 6×6×6): indici 16-231
	if idx >= 16 && idx <= 231 {
		idx -= 16
		r := uint8((idx / 36) * 51)
		g := uint8(((idx % 36) / 6) * 51)
		b := uint8((idx % 6) * 51)
		return r, g, b
	}

	// 24 livelli di grigio: indici 232-255
	if idx >= 232 && idx <= 255 {
		v := uint8(8 + (idx-232)*10)
		return v, v, v
	}

	// Fallback
	if isFG {
		return Palette16[DefaultFG][0], Palette16[DefaultFG][1], Palette16[DefaultFG][2]
	}
	return 0, 0, 0
}

// ─────────────────────────────────────────────
// CellAttr — attributi grafici di una cella
// ─────────────────────────────────────────────

// CellAttr contiene gli attributi grafici di una cella del terminale.
// Equivalente della classe CellAttr Python.
type CellAttr struct {
	FG        Color
	BG        Color
	Bold      bool
	Blink     bool
	Reverse   bool
	Underline bool
}

// DefaultAttr ritorna un CellAttr con valori di default.
func DefaultAttr() CellAttr {
	return CellAttr{
		FG: IndexColor(DefaultFG),
		BG: IndexColor(DefaultBG),
	}
}

// Copy ritorna una copia dell'attributo.
func (a CellAttr) Copy() CellAttr {
	return a // struct value → già una copia
}

// ─────────────────────────────────────────────
// Cell — una cella del terminale
// ─────────────────────────────────────────────

// Cell rappresenta una singola cella del terminale (carattere + attributi).
type Cell struct {
	Char rune
	Attr CellAttr
}

// NewCell crea una cella vuota con attributi di default.
func NewCell() Cell {
	return Cell{Char: ' ', Attr: DefaultAttr()}
}

// ─────────────────────────────────────────────
// Screen — emulatore terminale ANSI
// ─────────────────────────────────────────────

// Stati del parser
const (
	stateNormal = iota
	stateESC    // ricevuto ESC
	stateCSI    // ricevuto ESC[
	stateOSC    // ricevuto ESC]
)

// Screen è l'emulatore terminale ANSI completo.
// Equivalente della classe AnsiScreen Python.
type Screen struct {
	Cols, Rows int
	CursorX    int
	CursorY    int
	Buffer     [][]Cell

	// Callback per risposte al server (DSR)
	OnResponse func(data []byte)

	attr    CellAttr
	savedX  int
	savedY  int
	state   int
	csiBuf  strings.Builder
}

// NewScreen crea uno Screen con le dimensioni date.
func NewScreen(cols, rows int) *Screen {
	s := &Screen{
		Cols: cols,
		Rows: rows,
		attr: DefaultAttr(),
	}
	s.Buffer = s.newBuffer()
	return s
}

func (s *Screen) newBuffer() [][]Cell {
	buf := make([][]Cell, s.Rows)
	for y := range buf {
		buf[y] = s.newRow()
	}
	return buf
}

func (s *Screen) newRow() []Cell {
	row := make([]Cell, s.Cols)
	for x := range row {
		row[x] = NewCell()
	}
	return row
}

// Reset riporta lo schermo allo stato iniziale.
func (s *Screen) Reset() {
	s.CursorX = 0
	s.CursorY = 0
	s.attr = DefaultAttr()
	s.state = stateNormal
	s.csiBuf.Reset()
	s.Buffer = s.newBuffer()
}

// ─────────────────────────────────────────────
// Feed — alimentazione testo
// ─────────────────────────────────────────────

// Feed processa una stringa di testo (già decodificata da CP437).
func (s *Screen) Feed(text string) {
	for _, ch := range text {
		s.process(ch)
	}
}

func (s *Screen) process(ch rune) {
	switch s.state {
	case stateNormal:
		switch {
		case ch == 0x1B: // ESC
			s.state = stateESC
		case ch == 0x0D: // CR
			s.CursorX = 0
		case ch == 0x0A: // LF
			s.lineFeed()
		case ch == 0x08: // BS
			if s.CursorX > 0 {
				s.CursorX--
			}
		case ch == 0x09: // TAB
			s.CursorX = min(s.CursorX+(8-s.CursorX%8), s.Cols-1)
		case ch == 0x07: // BEL
			// ignora
		case ch >= 0x20: // stampabile
			s.putChar(ch)
		}

	case stateESC:
		switch ch {
		case '[':
			s.state = stateCSI
			s.csiBuf.Reset()
		case ']':
			s.state = stateOSC
			s.csiBuf.Reset()
		case 'D': // Index
			s.lineFeed()
			s.state = stateNormal
		case 'M': // Reverse Index
			s.reverseLF()
			s.state = stateNormal
		case 'E': // Next Line
			s.CursorX = 0
			s.lineFeed()
			s.state = stateNormal
		case '7': // Save cursor (DEC)
			s.savedX = s.CursorX
			s.savedY = s.CursorY
			s.state = stateNormal
		case '8': // Restore cursor (DEC)
			s.CursorX = s.savedX
			s.CursorY = s.savedY
			s.state = stateNormal
		case 'c': // Reset
			s.Reset()
		default:
			s.state = stateNormal
		}

	case stateCSI:
		if (ch >= '0' && ch <= '9') || ch == ';' || ch == '?' {
			if s.csiBuf.Len() < MaxCSIBuf {
				s.csiBuf.WriteRune(ch)
			} else {
				// Buffer troppo lungo → reset (FIND-006)
				s.state = stateNormal
				s.csiBuf.Reset()
			}
		} else {
			s.execCSI(ch)
			s.state = stateNormal
		}

	case stateOSC:
		if ch == 0x07 || ch == 0x1B {
			s.state = stateNormal
		}
	}
}

// ─────────────────────────────────────────────
// Carattere stampabile
// ─────────────────────────────────────────────

func (s *Screen) putChar(ch rune) {
	if s.CursorX >= s.Cols {
		s.CursorX = 0
		s.lineFeed()
	}
	s.Buffer[s.CursorY][s.CursorX].Char = ch
	s.Buffer[s.CursorY][s.CursorX].Attr = s.attr.Copy()
	s.CursorX++
}

// ─────────────────────────────────────────────
// Scroll
// ─────────────────────────────────────────────

func (s *Screen) lineFeed() {
	if s.CursorY < s.Rows-1 {
		s.CursorY++
	} else {
		// Scroll up: rimuovi prima riga, aggiungi nuova in fondo
		copy(s.Buffer, s.Buffer[1:])
		s.Buffer[s.Rows-1] = s.newRow()
	}
}

func (s *Screen) reverseLF() {
	if s.CursorY > 0 {
		s.CursorY--
	} else {
		// Scroll down: rimuovi ultima riga, inserisci nuova in cima
		copy(s.Buffer[1:], s.Buffer)
		s.Buffer[0] = s.newRow()
	}
}

// ─────────────────────────────────────────────
// Parsing parametri CSI
// ─────────────────────────────────────────────

func (s *Screen) parseParams(defaultVal int) []int {
	raw := s.csiBuf.String()
	raw = strings.TrimLeft(raw, "?")

	if raw == "" {
		return []int{defaultVal}
	}

	parts := strings.Split(raw, ";")
	result := make([]int, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			result = append(result, defaultVal)
		} else {
			v, err := strconv.Atoi(p)
			if err != nil {
				result = append(result, defaultVal)
			} else {
				result = append(result, v)
			}
		}
	}
	return result
}

// ─────────────────────────────────────────────
// Esecuzione comandi CSI
// ─────────────────────────────────────────────

func (s *Screen) execCSI(cmd rune) {
	params := s.parseParams(0)

	switch cmd {
	case 'm': // SGR — colori e attributi
		s.sgr(params)

	case 'H', 'f': // Cursor Position
		r := max(1, safeParam(params, 0, 1))
		c := max(1, safeParam(params, 1, 1))
		s.CursorY = min(r-1, s.Rows-1)
		s.CursorX = min(c-1, s.Cols-1)

	case 'A': // Cursor Up
		s.CursorY = max(0, s.CursorY-max(1, params[0]))

	case 'B': // Cursor Down
		s.CursorY = min(s.Rows-1, s.CursorY+max(1, params[0]))

	case 'C': // Cursor Forward
		s.CursorX = min(s.Cols-1, s.CursorX+max(1, params[0]))

	case 'D': // Cursor Back
		s.CursorX = max(0, s.CursorX-max(1, params[0]))

	case 'E': // Cursor Next Line
		s.CursorX = 0
		s.CursorY = min(s.Rows-1, s.CursorY+max(1, params[0]))

	case 'F': // Cursor Previous Line
		s.CursorX = 0
		s.CursorY = max(0, s.CursorY-max(1, params[0]))

	case 'G': // Cursor Horizontal Absolute
		s.CursorX = min(max(1, params[0])-1, s.Cols-1)

	case 'J': // Erase in Display
		s.eraseDisplay(params[0])

	case 'K': // Erase in Line
		s.eraseLine(params[0])

	case 'S': // Scroll Up
		for range max(1, params[0]) {
			copy(s.Buffer, s.Buffer[1:])
			s.Buffer[s.Rows-1] = s.newRow()
		}

	case 'T': // Scroll Down
		for range max(1, params[0]) {
			copy(s.Buffer[1:], s.Buffer)
			s.Buffer[0] = s.newRow()
		}

	case 's': // Save Cursor
		s.savedX = s.CursorX
		s.savedY = s.CursorY

	case 'u': // Restore Cursor
		s.CursorX = s.savedX
		s.CursorY = s.savedY

	case 'n': // Device Status Report (DSR)
		if params[0] == 6 && s.OnResponse != nil {
			// Report Cursor Position (la BBS usa questo per verificare ANSI)
			resp := []byte("\x1b[" + strconv.Itoa(s.CursorY+1) + ";" + strconv.Itoa(s.CursorX+1) + "R")
			s.OnResponse(resp)
		} else if params[0] == 5 && s.OnResponse != nil {
			s.OnResponse([]byte("\x1b[0n")) // Terminal OK
		}
	}
}

// ─────────────────────────────────────────────
// SGR (Select Graphic Rendition)
// ─────────────────────────────────────────────

func (s *Screen) sgr(params []int) {
	i := 0
	n := len(params)

	for i < n {
		p := params[i]

		switch {
		case p == 0: // Reset
			s.attr = DefaultAttr()
		case p == 1: // Bold
			s.attr.Bold = true
		case p == 2: // Dim
			s.attr.Bold = false
		case p == 4: // Underline
			s.attr.Underline = true
		case p == 5 || p == 6: // Blink
			s.attr.Blink = true
		case p == 7: // Reverse
			s.attr.Reverse = true
		case p == 22: // Normal intensity
			s.attr.Bold = false
		case p == 24: // No underline
			s.attr.Underline = false
		case p == 25: // No blink
			s.attr.Blink = false
		case p == 27: // No reverse
			s.attr.Reverse = false

		// Foreground standard (30-37)
		case p >= 30 && p <= 37:
			s.attr.FG = IndexColor(p - 30)

		// Foreground esteso (38;5;n / 38;2;r;g;b)
		case p == 38:
			if i+1 < n && params[i+1] == 5 && i+2 < n {
				s.attr.FG = IndexColor(params[i+2])
				i += 2
			} else if i+1 < n && params[i+1] == 2 && i+4 < n {
				s.attr.FG = RGBColor(uint8(params[i+2]), uint8(params[i+3]), uint8(params[i+4]))
				i += 4
			}

		case p == 39: // Default foreground
			s.attr.FG = IndexColor(DefaultFG)

		// Background standard (40-47)
		case p >= 40 && p <= 47:
			s.attr.BG = IndexColor(p - 40)

		// Background esteso (48;5;n / 48;2;r;g;b)
		case p == 48:
			if i+1 < n && params[i+1] == 5 && i+2 < n {
				s.attr.BG = IndexColor(params[i+2])
				i += 2
			} else if i+1 < n && params[i+1] == 2 && i+4 < n {
				s.attr.BG = RGBColor(uint8(params[i+2]), uint8(params[i+3]), uint8(params[i+4]))
				i += 4
			}

		case p == 49: // Default background
			s.attr.BG = IndexColor(DefaultBG)

		// Bright foreground (90-97)
		case p >= 90 && p <= 97:
			s.attr.FG = IndexColor(p - 90 + 8)

		// Bright background (100-107)
		case p >= 100 && p <= 107:
			s.attr.BG = IndexColor(p - 100 + 8)
		}

		i++
	}
}

// ─────────────────────────────────────────────
// Erase helpers
// ─────────────────────────────────────────────

func (s *Screen) eraseDisplay(mode int) {
	switch mode {
	case 0: // dal cursore alla fine
		for x := s.CursorX; x < s.Cols; x++ {
			s.Buffer[s.CursorY][x] = NewCell()
		}
		for y := s.CursorY + 1; y < s.Rows; y++ {
			s.Buffer[y] = s.newRow()
		}
	case 1: // dall'inizio al cursore
		for x := 0; x <= s.CursorX; x++ {
			s.Buffer[s.CursorY][x] = NewCell()
		}
		for y := 0; y < s.CursorY; y++ {
			s.Buffer[y] = s.newRow()
		}
	case 2: // tutto lo schermo
		s.Buffer = s.newBuffer()
	}
}

func (s *Screen) eraseLine(mode int) {
	switch mode {
	case 0: // dal cursore alla fine riga
		for x := s.CursorX; x < s.Cols; x++ {
			s.Buffer[s.CursorY][x] = NewCell()
		}
	case 1: // dall'inizio riga al cursore
		for x := 0; x <= s.CursorX; x++ {
			s.Buffer[s.CursorY][x] = NewCell()
		}
	case 2: // tutta la riga
		s.Buffer[s.CursorY] = s.newRow()
	}
}

// ─────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────

func safeParam(params []int, index, defaultVal int) int {
	if index < len(params) {
		return params[index]
	}
	return defaultVal
}
