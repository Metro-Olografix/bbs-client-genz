// Package telnet implementa una connessione Telnet con negoziazione IAC
// completa per BBS (NAWS, TTYPE, ECHO, SGA, BINARY).
//
// Porting da bbs_client.py (Python/PyQt5) → Go idiomatico.
// Thread Python → goroutine, signal/slot Qt → channels.
package telnet

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rj45lab/bbs-client-go/internal/zmodem"
)

// ─────────────────────────────────────────────
// Costanti protocollo Telnet (RFC 854)
// ─────────────────────────────────────────────

const (
	IAC    byte = 255
	DONT   byte = 254
	DO     byte = 253
	WONT   byte = 252
	WILL   byte = 251
	SB     byte = 250
	SE     byte = 240
	NAWS   byte = 31
	TTYPE  byte = 24
	ECHO   byte = 1
	SGA    byte = 3
	BINARY byte = 0
)

// Configurazione di default
const (
	DefaultHost    = "bbs.olografix.org"
	DefaultPort    = 23
	DefaultCols    = 80
	DefaultRows    = 25
	ConnectTimeout = 15 * time.Second
	ReadTimeout    = 500 * time.Millisecond
	RecvBufSize    = 8192
)

// TermType inviato durante la negoziazione TTYPE
var TermType = []byte("ANSI")

// ─────────────────────────────────────────────
// Connection — connessione Telnet verso BBS
// ─────────────────────────────────────────────

// Connection gestisce la connessione TCP/Telnet verso la BBS.
// Equivalente Go di TelnetConnection(QObject) nel codice Python.
//
// Il pattern è: goroutine di ricezione → channel DataCh per i dati puliti,
// invece di signal/slot Qt.
type Connection struct {
	// Canali di output (equivalenti ai pyqtSignal)
	DataCh    chan []byte // dati puliti (senza IAC) → terminale
	EventCh   chan Event  // eventi connessione (connected, lost, error)

	// Configurazione terminale
	Cols int
	Rows int

	// Debug
	Debug bool

	conn      net.Conn
	mu        sync.Mutex
	connected bool
	stopCh    chan struct{}

	// ZMODEM state
	zmodemReceiver  *zmodem.Receiver
	zmodemSender    *zmodem.Sender
	zmodemActive    bool
	zmodemDetectBuf []byte
	downloadDir     string
}

// EventType identifica il tipo di evento di connessione
type EventType int

const (
	EventConnected    EventType = iota
	EventDisconnected
	EventError
	EventZmodemStarted  // filename, filesize
	EventZmodemProgress // bytes, total, speed
	EventZmodemFinished // filepath, success
	EventZmodemError    // error message
)

// Event rappresenta un evento di connessione
type Event struct {
	Type    EventType
	Message string
	// Campi extra per eventi ZMODEM
	Filename string
	Filepath string
	Filesize int64
	Bytes    int64
	Speed    float64
	Success  bool
}

// New crea una nuova Connection con configurazione di default.
func New() *Connection {
	// Directory download: ./downloads relativa all'eseguibile
	exe, _ := os.Executable()
	dlDir := filepath.Join(filepath.Dir(exe), "downloads")

	return &Connection{
		DataCh:      make(chan []byte, 64),
		EventCh:     make(chan Event, 16),
		Cols:        DefaultCols,
		Rows:        DefaultRows,
		stopCh:      make(chan struct{}),
		downloadDir: dlDir,
	}
}

// SetDownloadDir imposta la directory di download.
func (c *Connection) SetDownloadDir(dir string) {
	c.downloadDir = dir
}

// Connected ritorna true se la connessione è attiva.
func (c *Connection) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Connect apre la connessione TCP verso host:port e avvia la goroutine
// di ricezione. Equivalente di connect_to() nel codice Python.
func (c *Connection) Connect(host string, port int) error {
	addr := fmt.Sprintf("%s:%d", host, port)

	if c.Debug {
		log.Printf("[TELNET] Connessione a %s...", addr)
	}

	conn, err := net.DialTimeout("tcp", addr, ConnectTimeout)
	if err != nil {
		c.EventCh <- Event{Type: EventError, Message: err.Error()}
		return err
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.stopCh = make(chan struct{})
	c.mu.Unlock()

	c.EventCh <- Event{Type: EventConnected, Message: addr}

	// Goroutine di ricezione (equivalente di _recv_loop in Python)
	go c.recvLoop()

	return nil
}

// Disconnect chiude la connessione. Equivalente di disconnect() Python.
func (c *Connection) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return
	}

	c.connected = false
	close(c.stopCh)

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// Send invia dati raw al server. Equivalente di send() Python.
func (c *Connection) Send(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.conn == nil {
		return fmt.Errorf("non connesso")
	}

	_, err := c.conn.Write(data)
	if err != nil {
		c.connected = false
		go func() {
			c.EventCh <- Event{Type: EventDisconnected, Message: err.Error()}
		}()
		return err
	}
	return nil
}

// ─────────────────────────────────────────────
// Loop di ricezione (goroutine)
// ─────────────────────────────────────────────

func (c *Connection) recvLoop() {
	buf := make([]byte, RecvBufSize)

	for {
		// Controlla se dobbiamo fermarci
		select {
		case <-c.stopCh:
			return
		default:
		}

		// Timeout di lettura per non bloccare indefinitamente
		c.conn.SetReadDeadline(time.Now().Add(ReadTimeout))

		n, err := c.conn.Read(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// ZMODEM timeout check (come Python FIND-010)
				if c.zmodemActive && c.zmodemReceiver != nil {
					elapsed := time.Since(c.zmodemReceiver.StartTime).Seconds()
					if elapsed > 300 {
						c.emitEvent(Event{Type: EventZmodemError, Message: "Timeout ZMODEM — superati 5 minuti"})
						c.zmodemReceiver.Cancel()
						c.zmodemActive = false
					} else if elapsed > 60 && c.zmodemReceiver.BytesReceived == 0 {
						c.emitEvent(Event{Type: EventZmodemError, Message: "Timeout ZMODEM — nessun dato ricevuto"})
						c.zmodemReceiver.Cancel()
						c.zmodemActive = false
					}
				}
				continue
			}
			// Connessione persa
			c.mu.Lock()
			wasConnected := c.connected
			c.connected = false
			c.mu.Unlock()

			if wasConnected {
				c.EventCh <- Event{
					Type:    EventDisconnected,
					Message: err.Error(),
				}
			}
			return
		}

		if n == 0 {
			c.mu.Lock()
			c.connected = false
			c.mu.Unlock()
			c.EventCh <- Event{
				Type:    EventDisconnected,
				Message: "Connessione chiusa dal server",
			}
			return
		}

		// Processa protocollo Telnet (rimuovi/gestisci IAC)
		clean := c.processTelnet(buf[:n])

		if len(clean) == 0 {
			continue
		}

		// ── ZMODEM: se attivo, devia dati al protocollo ──
		if c.zmodemActive {
			if c.zmodemReceiver != nil && c.zmodemReceiver.State != zmodem.RxIdle &&
				c.zmodemReceiver.State != zmodem.RxDone {
				c.zmodemReceiver.Feed(clean)
			} else if c.zmodemSender != nil && c.zmodemSender.State != zmodem.TxIdle &&
				c.zmodemSender.State != zmodem.TxDone {
				c.zmodemSender.Feed(clean)
			} else {
				// ZMODEM finito, torna al terminale
				c.zmodemActive = false
				c.emitData(clean)
			}
			continue
		}

		// ── ZMODEM: auto-detect (con buffer cross-recv) ──
		detectData := append(c.zmodemDetectBuf, clean...)

		if zmodem.Detect(detectData) {
			if c.Debug {
				log.Printf("[ZMODEM] *** DETECT! Avvio download")
			}
			c.zmodemDetectBuf = nil
			c.startZmodemDownload(detectData)
			continue
		}

		// Mantieni ultimi 64 byte per il prossimo ciclo
		if len(clean) >= 64 {
			c.zmodemDetectBuf = clean[len(clean)-64:]
		} else {
			c.zmodemDetectBuf = make([]byte, len(clean))
			copy(c.zmodemDetectBuf, clean)
		}

		// Invia dati puliti al channel
		c.emitData(clean)
	}
}

func (c *Connection) emitData(data []byte) {
	select {
	case c.DataCh <- data:
	default:
		if c.Debug {
			log.Printf("[TELNET] DataCh pieno, drop %d bytes", len(data))
		}
	}
}

func (c *Connection) emitEvent(e Event) {
	select {
	case c.EventCh <- e:
	default:
	}
}

// ─────────────────────────────────────────────
// ZMODEM integration
// ─────────────────────────────────────────────

func (c *Connection) zmodemSendData(data []byte) {
	c.Send(data)
}

func (c *Connection) zmodemLog(msg string) {
	if c.Debug {
		log.Printf("[ZMODEM] %s", msg)
	}
}

func (c *Connection) startZmodemDownload(initialData []byte) {
	os.MkdirAll(c.downloadDir, 0755)

	rx := zmodem.NewReceiver(c.downloadDir, c.zmodemSendData, c.zmodemLog)

	rx.OnStart = func(filename string, filesize int64) {
		c.emitEvent(Event{Type: EventZmodemStarted, Filename: filename, Filesize: filesize})
	}
	rx.OnProgress = func(received, total int64, speed float64) {
		c.emitEvent(Event{Type: EventZmodemProgress, Bytes: received, Filesize: total, Speed: speed})
	}
	rx.OnComplete = func(fp string) {
		c.emitEvent(Event{Type: EventZmodemFinished, Filepath: fp, Success: true})
	}
	rx.OnError = func(msg string) {
		c.emitEvent(Event{Type: EventZmodemError, Message: msg})
	}
	rx.OnFinished = func() {
		c.zmodemActive = false
		c.zmodemReceiver = nil
		c.zmodemSender = nil
	}

	c.zmodemReceiver = rx
	c.zmodemActive = true
	rx.Start(initialData)
}

// StartZmodemUpload avvia upload ZMODEM di un file.
func (c *Connection) StartZmodemUpload(filepath string) {
	tx := zmodem.NewSender(c.zmodemSendData, c.zmodemLog)

	tx.OnStart = func(filename string, filesize int64) {
		c.emitEvent(Event{Type: EventZmodemStarted, Filename: filename, Filesize: filesize})
	}
	tx.OnProgress = func(sent, total int64, speed float64) {
		c.emitEvent(Event{Type: EventZmodemProgress, Bytes: sent, Filesize: total, Speed: speed})
	}
	tx.OnComplete = func(fp string) {
		c.emitEvent(Event{Type: EventZmodemFinished, Filepath: fp, Success: true})
	}
	tx.OnError = func(msg string) {
		c.emitEvent(Event{Type: EventZmodemError, Message: msg})
	}
	tx.OnFinished = func() {
		c.zmodemActive = false
		c.zmodemReceiver = nil
		c.zmodemSender = nil
	}

	c.zmodemSender = tx
	c.zmodemActive = true
	tx.StartUpload(filepath)
}

// CancelZmodem annulla il trasferimento ZMODEM in corso.
func (c *Connection) CancelZmodem() {
	if c.zmodemReceiver != nil {
		c.zmodemReceiver.Cancel()
	}
	if c.zmodemSender != nil {
		c.zmodemSender.Cancel()
	}
	c.zmodemActive = false
}

// ─────────────────────────────────────────────
// Protocollo Telnet — parsing IAC
// ─────────────────────────────────────────────

// processTelnet processa i dati raw dal socket, gestisce le sequenze IAC
// e ritorna i dati puliti. Equivalente di _process_telnet() Python.
func (c *Connection) processTelnet(data []byte) []byte {
	clean := make([]byte, 0, len(data))
	i := 0
	n := len(data)

	for i < n {
		b := data[i]

		if b == IAC {
			if i+1 >= n {
				break
			}
			cmd := data[i+1]

			switch cmd {
			case IAC:
				// IAC IAC → byte 255 letterale
				clean = append(clean, IAC)
				i += 2

			case DO, DONT, WILL, WONT:
				if i+2 >= n {
					break
				}
				c.negotiate(cmd, data[i+2])
				i += 3

			case SB:
				// Cerca IAC SE per la fine della subnegotiation
				end := findIACSE(data, i)
				if end == -1 {
					// Subnegotiation incompleta, interrompi
					break
				}
				c.subnegotiate(data[i+2 : end])
				i = end + 2

			default:
				i += 2
			}
		} else {
			clean = append(clean, b)
			i++
		}
	}

	return clean
}

// findIACSE cerca la posizione di IAC SE (255, 240) in data a partire da start.
func findIACSE(data []byte, start int) int {
	for i := start; i < len(data)-1; i++ {
		if data[i] == IAC && data[i+1] == SE {
			return i
		}
	}
	return -1
}

// ─────────────────────────────────────────────
// Negoziazione Telnet
// ─────────────────────────────────────────────

// negotiate gestisce DO/DONT/WILL/WONT. Equivalente di _negotiate() Python.
func (c *Connection) negotiate(cmd, opt byte) {
	if c.Debug {
		log.Printf("[TELNET] Negoziazione: cmd=%d opt=%d", cmd, opt)
	}

	switch cmd {
	case DO:
		switch opt {
		case TTYPE:
			c.sendIAC(WILL, TTYPE)
		case NAWS:
			c.sendIAC(WILL, NAWS)
			c.sendNAWS()
		case SGA, BINARY:
			c.sendIAC(WILL, opt)
		default:
			c.sendIAC(WONT, opt)
		}

	case WILL:
		switch opt {
		case ECHO, SGA, BINARY:
			c.sendIAC(DO, opt)
		default:
			c.sendIAC(DONT, opt)
		}

	case DONT:
		c.sendIAC(WONT, opt)

	case WONT:
		c.sendIAC(DONT, opt)
	}
}

// subnegotiate gestisce le sotto-negoziazioni (SB...SE).
// Equivalente di _subnegotiate() Python.
func (c *Connection) subnegotiate(data []byte) {
	if len(data) >= 2 && data[0] == TTYPE && data[1] == 1 {
		// Server chiede il tipo di terminale → rispondiamo "ANSI"
		resp := make([]byte, 0, 4+len(TermType)+2)
		resp = append(resp, IAC, SB, TTYPE, 0)
		resp = append(resp, TermType...)
		resp = append(resp, IAC, SE)
		c.Send(resp)

		if c.Debug {
			log.Printf("[TELNET] TTYPE → %s", TermType)
		}
	}
}

// sendIAC invia un comando IAC cmd opt.
func (c *Connection) sendIAC(cmd, opt byte) {
	c.Send([]byte{IAC, cmd, opt})
}

// sendNAWS invia la dimensione della finestra (NAWS).
// Equivalente di _send_naws() Python.
func (c *Connection) sendNAWS() {
	buf := make([]byte, 9)
	buf[0] = IAC
	buf[1] = SB
	buf[2] = NAWS
	binary.BigEndian.PutUint16(buf[3:5], uint16(c.Cols))
	binary.BigEndian.PutUint16(buf[5:7], uint16(c.Rows))
	buf[7] = IAC
	buf[8] = SE
	c.Send(buf)

	if c.Debug {
		log.Printf("[TELNET] NAWS → %dx%d", c.Cols, c.Rows)
	}
}
