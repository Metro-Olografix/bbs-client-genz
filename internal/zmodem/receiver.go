package zmodem

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ─────────────────────────────────────────────
// Receiver — Download handler (stato macchina)
// IDLE → INIT → WAIT_ZFILE → RECEIVING → DONE
// ─────────────────────────────────────────────

// ReceiverState rappresenta lo stato della macchina a stati del receiver
type ReceiverState int

const (
	RxIdle      ReceiverState = iota
	RxInit                    // ZRQINIT ricevuto, ZRINIT inviato
	RxWaitZFile               // In attesa di ZFILE
	RxReceiving               // Ricezione dati
	RxDone                    // Trasferimento completato
)

// Receiver gestisce il download ZMODEM (ricezione file dal server).
type Receiver struct {
	// Configurazione
	DownloadDir string
	SendFunc    func([]byte) // callback per inviare dati al server
	LogFunc     func(string) // callback log diagnostico

	// Stato
	State         ReceiverState
	UseCRC32      bool
	Filename      string
	Filepath      string
	Filesize      int64
	BytesReceived int64
	StartTime     time.Time

	// Callback UI
	OnStart    func(filename string, filesize int64)
	OnProgress func(received, total int64, speedKBs float64)
	OnComplete func(filepath string)
	OnError    func(message string)
	OnFinished func() // sessione ZMODEM terminata

	fileHandle *os.File
	buf        []byte
}

// NewReceiver crea un nuovo Receiver.
func NewReceiver(downloadDir string, sendFunc func([]byte), logFunc func(string)) *Receiver {
	if logFunc == nil {
		logFunc = func(string) {}
	}
	return &Receiver{
		DownloadDir: downloadDir,
		SendFunc:    sendFunc,
		LogFunc:     logFunc,
		State:       RxIdle,
	}
}

// Start avvia la ricezione ZMODEM con i dati iniziali.
func (r *Receiver) Start(initialData []byte) {
	os.MkdirAll(r.DownloadDir, 0755)
	r.State = RxInit
	r.StartTime = time.Now()
	r.buf = make([]byte, len(initialData))
	copy(r.buf, initialData)

	// Invia ZRINIT: pronti a ricevere, supporto CRC32 e full-duplex
	flags := CANFDX | CANOVIO | CANFC32
	zrinit := BuildHexHeader(ZRINIT, 0, 0, 0, flags)
	r.LogFunc(fmt.Sprintf("[RX] Invio ZRINIT: %q", zrinit))
	r.SendFunc(zrinit)
	r.State = RxWaitZFile

	r.processBuffer()
}

// Feed alimenta dati ricevuti dal socket al receiver.
func (r *Receiver) Feed(data []byte) {
	if r.State == RxIdle || r.State == RxDone {
		return
	}
	r.LogFunc(fmt.Sprintf("[RX] feed %dB state=%d buf=%d", len(data), r.State, len(r.buf)))
	r.buf = append(r.buf, data...)
	r.processBuffer()
}

// Cancel annulla il trasferimento corrente.
func (r *Receiver) Cancel() {
	r.SendFunc(AbortSeq)
	r.cleanup()
	r.State = RxDone
	if r.OnFinished != nil {
		r.OnFinished()
	}
}

func (r *Receiver) cleanup() {
	if r.fileHandle != nil {
		r.fileHandle.Close()
		r.fileHandle = nil
	}
}

func (r *Receiver) processBuffer() {
	for iteration := 0; len(r.buf) > 0 && iteration < 200; iteration++ {
		switch r.State {
		case RxWaitZFile, RxInit:
			if !r.tryParseHeader() {
				return
			}
		case RxReceiving:
			if !r.tryParseData() {
				return
			}
		case RxDone:
			return
		}
	}
}

func (r *Receiver) tryParseHeader() bool {
	data := r.buf
	r.LogFunc(fmt.Sprintf("[RX] tryParseHeader buf=%dB", len(data)))

	// Prova hex header
	if hdr := ParseHexHeader(data); hdr != nil {
		r.LogFunc(fmt.Sprintf("[RX] HEX HEADER: type=%d p=[%d,%d,%d,%d] consumed=%d",
			hdr.FrameType, hdr.P0, hdr.P1, hdr.P2, hdr.P3, hdr.Consumed))
		r.buf = r.buf[hdr.Consumed:]
		r.handleHeader(hdr.FrameType, hdr.P0, hdr.P1, hdr.P2, hdr.P3)
		return true
	}

	// Prova binary header
	if hdr := ParseBinHeader(data); hdr != nil {
		r.LogFunc(fmt.Sprintf("[RX] BIN HEADER: type=%d p=[%d,%d,%d,%d] consumed=%d crc32=%v",
			hdr.FrameType, hdr.P0, hdr.P1, hdr.P2, hdr.P3, hdr.Consumed, hdr.IsCRC32))
		r.buf = r.buf[hdr.Consumed:]
		if hdr.IsCRC32 {
			r.UseCRC32 = true
		}
		r.handleHeader(hdr.FrameType, hdr.P0, hdr.P1, hdr.P2, hdr.P3)
		return true
	}

	// Nessun header — scarta se buffer troppo grande
	if len(r.buf) > 1024 {
		for i := 1; i < len(r.buf); i++ {
			if r.buf[i] == ZPAD {
				r.buf = r.buf[i:]
				return true
			}
		}
		r.buf = r.buf[:0]
	}
	return false
}

func (r *Receiver) tryParseData() bool {
	data := r.buf
	r.LogFunc(fmt.Sprintf("[RX] tryParseData buf=%dB crc32=%v", len(data), r.UseCRC32))

	// Controlla prima se c'è un header (ZEOF, ZFIN, ecc.)
	if hdr := ParseHexHeader(data); hdr != nil {
		r.LogFunc(fmt.Sprintf("[RX] DATA-HEX HEADER: type=%d consumed=%d", hdr.FrameType, hdr.Consumed))
		r.buf = r.buf[hdr.Consumed:]
		r.handleHeader(hdr.FrameType, hdr.P0, hdr.P1, hdr.P2, hdr.P3)
		return true
	}

	if hdr := ParseBinHeader(data); hdr != nil {
		r.LogFunc(fmt.Sprintf("[RX] DATA-BIN HEADER: type=%d consumed=%d crc32=%v",
			hdr.FrameType, hdr.Consumed, hdr.IsCRC32))
		r.buf = r.buf[hdr.Consumed:]
		if hdr.IsCRC32 {
			r.UseCRC32 = true
		}
		r.handleHeader(hdr.FrameType, hdr.P0, hdr.P1, hdr.P2, hdr.P3)
		return true
	}

	// Prova subpacket dati
	if sp := ParseDataSubpacket(data, r.UseCRC32); sp != nil {
		r.LogFunc(fmt.Sprintf("[RX] DATA SUBPACKET: %dB end=0x%02x consumed=%d",
			len(sp.Payload), sp.EndType, sp.Consumed))
		r.buf = r.buf[sp.Consumed:]
		r.handleData(sp.Payload, sp.EndType)
		return true
	}

	return false
}

func (r *Receiver) handleHeader(ftype, p0, p1, p2, p3 byte) {
	name := FrameNames[ftype]
	if name == "" {
		name = fmt.Sprintf("0x%02x", ftype)
	}
	r.LogFunc(fmt.Sprintf("[RX] HEADER: %s p=[%d,%d,%d,%d]", name, p0, p1, p2, p3))

	switch ftype {
	case ZRQINIT:
		flags := CANFDX | CANOVIO | CANFC32
		r.SendFunc(BuildHexHeader(ZRINIT, 0, 0, 0, flags))
		r.State = RxWaitZFile

	case ZFILE:
		r.State = RxReceiving

	case ZDATA:
		offset := PositionFromParams(p0, p1, p2, p3)
		if r.fileHandle != nil && int64(offset) != r.BytesReceived {
			r.fileHandle.Seek(int64(offset), 0)
			r.BytesReceived = int64(offset)
		}
		r.State = RxReceiving

	case ZEOF:
		r.cleanup()
		if r.OnComplete != nil && r.Filepath != "" {
			r.OnComplete(r.Filepath)
		}
		flags := CANFDX | CANOVIO | CANFC32
		r.SendFunc(BuildHexHeader(ZRINIT, 0, 0, 0, flags))
		r.State = RxWaitZFile

	case ZFIN:
		r.SendFunc(BuildHexHeader(ZFIN, 0, 0, 0, 0))
		r.cleanup()
		r.State = RxDone
		if r.OnFinished != nil {
			r.OnFinished()
		}

	case ZSINIT:
		r.SendFunc(BuildHexHeader(ZACK, 0, 0, 0, 0))

	case ZCAN:
		r.cleanup()
		r.State = RxDone
		if r.OnError != nil {
			r.OnError("Trasferimento annullato dal server")
		}
		if r.OnFinished != nil {
			r.OnFinished()
		}
	}
}

func (r *Receiver) handleData(payload []byte, endType byte) {
	if len(payload) == 0 {
		return
	}

	// Se non abbiamo ancora il file aperto, questo è il subpacket ZFILE info
	if r.fileHandle == nil {
		r.LogFunc(fmt.Sprintf("[RX] FILE INFO subpacket: %q", payload[:min(80, len(payload))]))
		r.parseFileInfo(payload)
		return
	}

	// Scrivi dati su file
	_, err := r.fileHandle.Write(payload)
	if err != nil {
		if r.OnError != nil {
			r.OnError(fmt.Sprintf("Errore scrittura: %v", err))
		}
		r.Cancel()
		return
	}
	r.BytesReceived += int64(len(payload))

	// Aggiorna progresso
	if r.OnProgress != nil {
		elapsed := time.Since(r.StartTime).Seconds()
		if elapsed < 0.1 {
			elapsed = 0.1
		}
		speed := float64(r.BytesReceived) / 1024.0 / elapsed
		r.OnProgress(r.BytesReceived, r.Filesize, speed)
	}

	// Rispondi con ACK se richiesto
	if endType == ZCRCQ || endType == ZCRCW {
		r.SendFunc(BuildPosHeader(ZACK, uint32(r.BytesReceived)))
	}
}

// sanitizeFilename usata per la validazione sicura
var safeFilenameRe = regexp.MustCompile(`[^a-zA-Z0-9._\-]`)

func (r *Receiver) parseFileInfo(data []byte) {
	// Formato: filename\0 size mtime mode serial\0
	parts := splitNull(data)
	if len(parts) == 0 {
		return
	}

	r.Filename = string(parts[0])

	// Parsa dimensione
	if len(parts) > 1 && len(parts[1]) > 0 {
		meta := strings.Fields(string(parts[1]))
		if len(meta) > 0 {
			var size int64
			_, err := fmt.Sscanf(meta[0], "%d", &size)
			if err == nil && size >= 0 && size <= MaxFileSize {
				r.Filesize = size
			}
		}
	}

	// SECURITY: sanitizzazione filename (FIND-002)
	r.Filename = strings.ReplaceAll(r.Filename, "\\", "/")
	r.Filename = filepath.Base(r.Filename)
	r.Filename = safeFilenameRe.ReplaceAllString(r.Filename, "_")
	if r.Filename == "" || r.Filename == "." || r.Filename == ".." || strings.HasPrefix(r.Filename, ".") {
		r.Filename = "download"
	}

	r.Filepath = filepath.Join(r.DownloadDir, r.Filename)

	// SECURITY: verifica path traversal
	realPath, _ := filepath.Abs(r.Filepath)
	realDownload, _ := filepath.Abs(r.DownloadDir)
	if !strings.HasPrefix(realPath, realDownload+string(filepath.Separator)) && realPath != realDownload {
		r.LogFunc(fmt.Sprintf("[RX] SECURITY: path traversal bloccato: %s", realPath))
		if r.OnError != nil {
			r.OnError(fmt.Sprintf("Path traversal bloccato: %s", r.Filename))
		}
		r.Cancel()
		return
	}

	// Gestisci file duplicati
	base := r.Filepath
	ext := filepath.Ext(base)
	nameOnly := strings.TrimSuffix(base, ext)
	counter := 1
	for {
		if _, err := os.Stat(r.Filepath); os.IsNotExist(err) {
			break
		}
		r.Filepath = fmt.Sprintf("%s_%d%s", nameOnly, counter, ext)
		counter++
	}

	// Apri file
	var err error
	r.fileHandle, err = os.Create(r.Filepath)
	if err != nil {
		if r.OnError != nil {
			r.OnError(fmt.Sprintf("Impossibile creare file: %v", err))
		}
		r.Cancel()
		return
	}
	r.BytesReceived = 0
	r.StartTime = time.Now()

	r.LogFunc(fmt.Sprintf("[RX] File aperto: %s size=%d", r.Filepath, r.Filesize))
	if r.OnStart != nil {
		r.OnStart(r.Filename, r.Filesize)
	}

	// Invia ZRPOS(0) — inizia dal byte 0
	r.SendFunc(BuildPosHeader(ZRPOS, 0))
	r.State = RxReceiving
}

func splitNull(data []byte) [][]byte {
	var parts [][]byte
	start := 0
	for i, b := range data {
		if b == 0 {
			parts = append(parts, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		parts = append(parts, data[start:])
	}
	return parts
}
