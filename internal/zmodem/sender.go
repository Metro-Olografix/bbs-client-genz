package zmodem

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ─────────────────────────────────────────────
// Sender — Upload handler (stato macchina)
// ─────────────────────────────────────────────

// SenderState rappresenta lo stato della macchina a stati del sender
type SenderState int

const (
	TxIdle      SenderState = iota
	TxWaitRInit             // In attesa ZRINIT dal server
	TxWaitZRPos             // ZFILE inviato, attendo ZRPOS
	TxSending               // Invio dati
	TxWaitAck               // In attesa conferma dopo ZEOF
	TxWaitZFin              // ZFIN inviato, attendo ZFIN dalla BBS
	TxDone
)

// Sender gestisce l'upload ZMODEM (invio file al server).
type Sender struct {
	// Configurazione
	SendFunc func([]byte)
	LogFunc  func(string)

	// Stato
	State    SenderState
	UseCRC32 bool

	// File corrente
	Filepath  string
	Filename  string
	Filesize  int64
	BytesSent int64
	StartTime time.Time

	// Callback UI
	OnStart    func(filename string, filesize int64)
	OnProgress func(sent, total int64, speedKBs float64)
	OnComplete func(filepath string)
	OnError    func(message string)
	OnFinished func()

	fileHandle *os.File
	buf        []byte
	retryCount int
}

// NewSender crea un nuovo Sender.
func NewSender(sendFunc func([]byte), logFunc func(string)) *Sender {
	if logFunc == nil {
		logFunc = func(string) {}
	}
	return &Sender{
		SendFunc: sendFunc,
		LogFunc:  logFunc,
		State:    TxIdle,
	}
}

// StartUpload avvia l'upload di un file.
func (s *Sender) StartUpload(path string) {
	s.LogFunc(fmt.Sprintf("[TX] start_upload: %s", path))

	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		s.LogFunc(fmt.Sprintf("[TX] ERRORE: file non trovato: %s", path))
		if s.OnError != nil {
			s.OnError(fmt.Sprintf("File non trovato: %s", path))
		}
		return
	}

	// SEC-008: verifica limite dimensione file
	if info.Size() > MaxFileSize {
		s.LogFunc(fmt.Sprintf("[TX] ERRORE: file troppo grande: %d > %d", info.Size(), MaxFileSize))
		if s.OnError != nil {
			s.OnError(fmt.Sprintf("File troppo grande: %d MB (max %d GB)",
				info.Size()/1024/1024, MaxFileSize/1024/1024/1024))
		}
		return
	}

	s.Filepath = path
	s.Filename = filepath.Base(path)
	s.Filesize = info.Size()
	s.BytesSent = 0
	s.retryCount = 0
	s.StartTime = time.Now()

	// Invia ZRQINIT per iniziare sessione
	zrqinit := BuildHexHeader(ZRQINIT, 0, 0, 0, 0)
	s.LogFunc(fmt.Sprintf("[TX] Invio ZRQINIT: %q", zrqinit))
	s.SendFunc(zrqinit)
	s.State = TxWaitRInit
}

// Feed alimenta dati ricevuti dal server.
func (s *Sender) Feed(data []byte) {
	if s.State == TxIdle || s.State == TxDone {
		return
	}
	s.LogFunc(fmt.Sprintf("[TX] feed %dB state=%d buf=%d", len(data), s.State, len(s.buf)))
	s.buf = append(s.buf, data...)

	// PT-002: protezione OOM
	if len(s.buf) > MaxBufSize {
		s.LogFunc(fmt.Sprintf("[TX] SECURITY: buffer overflow (%d > %d), annullo", len(s.buf), MaxBufSize))
		if s.OnError != nil {
			s.OnError("Buffer overflow: dati non validi dal server")
		}
		s.Cancel()
		return
	}

	s.processBuffer()
}

// Cancel annulla l'upload.
func (s *Sender) Cancel() {
	s.SendFunc(AbortSeq)
	s.cleanup()
	s.State = TxDone
	if s.OnFinished != nil {
		s.OnFinished()
	}
}

func (s *Sender) cleanup() {
	if s.fileHandle != nil {
		s.fileHandle.Close()
		s.fileHandle = nil
	}
}

func (s *Sender) processBuffer() {
	data := s.buf
	s.LogFunc(fmt.Sprintf("[TX] processBuffer %dB", len(data)))

	if hdr := ParseHexHeader(data); hdr != nil {
		s.LogFunc(fmt.Sprintf("[TX] HEX HEADER: type=%d p=[%d,%d,%d,%d] consumed=%d",
			hdr.FrameType, hdr.P0, hdr.P1, hdr.P2, hdr.P3, hdr.Consumed))
		s.buf = s.buf[hdr.Consumed:]
		s.handleHeader(hdr.FrameType, hdr.P0, hdr.P1, hdr.P2, hdr.P3)
		return
	}

	if hdr := ParseBinHeader(data); hdr != nil {
		s.LogFunc(fmt.Sprintf("[TX] BIN HEADER: type=%d p=[%d,%d,%d,%d] consumed=%d",
			hdr.FrameType, hdr.P0, hdr.P1, hdr.P2, hdr.P3, hdr.Consumed))
		s.buf = s.buf[hdr.Consumed:]
		s.handleHeader(hdr.FrameType, hdr.P0, hdr.P1, hdr.P2, hdr.P3)
		return
	}
}

func (s *Sender) handleHeader(ftype, p0, p1, p2, p3 byte) {
	name := FrameNames[ftype]
	if name == "" {
		name = fmt.Sprintf("0x%02x", ftype)
	}
	s.LogFunc(fmt.Sprintf("[TX] HEADER: %s p=[%d,%d,%d,%d] state=%d", name, p0, p1, p2, p3, s.State))

	switch ftype {
	case ZRINIT:
		// Server pronto a ricevere (ZF0 = p3 nel protocollo ZMODEM)
		s.UseCRC32 = (p3 & CANFC32) != 0
		s.LogFunc(fmt.Sprintf("[TX] ZRINIT: useCRC32=%v", s.UseCRC32))

		switch s.State {
		case TxWaitRInit:
			s.sendZFile()
			s.State = TxWaitZRPos
		case TxWaitZRPos:
			// BBS ha ri-inviato ZRINIT — ignoriamo
			s.LogFunc("[TX] ZRINIT ignorato in WAIT_ZRPOS")
		case TxWaitAck:
			// File completato
			s.LogFunc("[TX] Upload completato, invio ZFIN")
			s.cleanup()
			if s.OnComplete != nil {
				s.OnComplete(s.Filepath)
			}
			s.SendFunc(BuildHexHeader(ZFIN, 0, 0, 0, 0))
			s.State = TxWaitZFin
		}

	case ZRPOS:
		offset := PositionFromParams(p0, p1, p2, p3)
		s.retryCount++
		s.LogFunc(fmt.Sprintf("[TX] ZRPOS offset=%d retry=%d/%d", offset, s.retryCount, MaxRetries))
		if s.retryCount > MaxRetries {
			if s.OnError != nil {
				s.OnError("Upload fallito: troppi retry dal server")
			}
			s.Cancel()
			return
		}
		s.startSending(offset)

	case ZACK:
		offset := PositionFromParams(p0, p1, p2, p3)
		s.LogFunc(fmt.Sprintf("[TX] ZACK offset=%d", offset))

	case ZSKIP:
		s.LogFunc("[TX] ZSKIP — file saltato dal server")
		s.cleanup()
		s.SendFunc(BuildHexHeader(ZFIN, 0, 0, 0, 0))
		s.State = TxDone
		if s.OnFinished != nil {
			s.OnFinished()
		}

	case ZFIN:
		// BBS ha confermato — rispondi con "OO" (Over and Out)
		s.LogFunc("[TX] ZFIN ricevuto — invio OO")
		s.SendFunc([]byte("OO"))
		s.State = TxDone
		if s.OnFinished != nil {
			s.OnFinished()
		}

	case ZCAN:
		s.LogFunc("[TX] ZCAN — upload annullato dal server")
		s.cleanup()
		s.State = TxDone
		if s.OnError != nil {
			s.OnError("Upload annullato dal server")
		}
		if s.OnFinished != nil {
			s.OnFinished()
		}
	}
}

func (s *Sender) sendZFile() {
	// Header ZFILE binario
	zfileHdr := BuildBinHeader(ZFILE, 0, 0, 0, 0, s.UseCRC32)

	// Subpacket con info: "filename\0size mtime mode\0"
	info := []byte(s.Filename)
	info = append(info, 0)
	info = append(info, []byte(fmt.Sprintf("%d 0 0", s.Filesize))...)
	info = append(info, 0)
	subpkt := BuildDataSubpacket(info, ZCRCW, s.UseCRC32)

	// Combina in un unico send
	combined := make([]byte, 0, len(zfileHdr)+len(subpkt))
	combined = append(combined, zfileHdr...)
	combined = append(combined, subpkt...)

	s.LogFunc(fmt.Sprintf("[TX] Invio ZFILE: %s (%d bytes)", s.Filename, s.Filesize))
	s.SendFunc(combined)

	if s.OnStart != nil {
		s.OnStart(s.Filename, s.Filesize)
	}
}

func (s *Sender) startSending(offset uint32) {
	s.LogFunc(fmt.Sprintf("[TX] startSending offset=%d", offset))

	// Chiudi eventuale file handle precedente (BUG-005: evita leak su retry/ZRPOS)
	s.cleanup()

	var err error
	s.fileHandle, err = os.Open(s.Filepath)
	if err != nil {
		if s.OnError != nil {
			s.OnError(fmt.Sprintf("Errore lettura file: %v", err))
		}
		s.Cancel()
		return
	}

	if offset > 0 {
		s.fileHandle.Seek(int64(offset), 0)
	}
	s.BytesSent = int64(offset)
	s.State = TxSending

	// Invia ZDATA header con posizione
	zdataHdr := BuildBinPosHeader(ZDATA, offset, s.UseCRC32)
	s.LogFunc(fmt.Sprintf("[TX] Invio ZDATA offset=%d", offset))
	s.SendFunc(zdataHdr)

	// Invia blocchi di dati
	block := make([]byte, BlockSize)
	blocksSent := 0

	for {
		n, err := s.fileHandle.Read(block)
		if n == 0 || err != nil {
			break
		}

		s.BytesSent += int64(n)
		blocksSent++

		// Ultimo blocco? usa ZCRCE, altrimenti ZCRCG
		endType := ZCRCG
		if s.BytesSent >= s.Filesize {
			endType = ZCRCE
		}

		s.SendFunc(BuildDataSubpacket(block[:n], endType, s.UseCRC32))

		// Aggiorna progresso
		if s.OnProgress != nil {
			elapsed := time.Since(s.StartTime).Seconds()
			if elapsed < 0.1 {
				elapsed = 0.1
			}
			speed := float64(s.BytesSent) / 1024.0 / elapsed
			s.OnProgress(s.BytesSent, s.Filesize, speed)
		}
	}

	// Fine file
	s.LogFunc(fmt.Sprintf("[TX] File inviato: %d blocchi, %d bytes", blocksSent, s.BytesSent))
	s.cleanup()
	s.SendFunc(BuildPosHeader(ZEOF, uint32(s.BytesSent)))
	s.State = TxWaitAck
}
