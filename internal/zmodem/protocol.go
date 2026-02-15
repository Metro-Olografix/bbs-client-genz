// Package zmodem implementa il protocollo ZMODEM per download/upload
// di file da/verso BBS via connessione telnet.
//
// Porting da zmodem.py (Python) → Go idiomatico.
//
// Supporta:
// - Auto-detect ZMODEM (rileva ZRQINIT dal server)
// - Download (receive) e Upload (send) con macchina a stati
// - CRC16 e CRC32
// - ZDLE escaping
// - Header hex e binary
// - Progress callback per UI
//
// Riferimento: Chuck Forsberg, ZMODEM Protocol Specification
package zmodem

import (
	"encoding/binary"
	"fmt"
)

// ─────────────────────────────────────────────
// Costanti protocollo ZMODEM
// ─────────────────────────────────────────────

const (
	// Caratteri speciali
	ZPAD  byte = 0x2A // '*' — padding
	ZDLE  byte = 0x18 // CAN — data link escape
	ZDLEE byte = 0x58 // ZDLE escaped (ZDLE ^ 0x40)

	// Tipi header
	ZHEX   byte = 0x42 // 'B' — hex header
	ZBIN   byte = 0x41 // 'A' — binary header CRC16
	ZBIN32 byte = 0x43 // 'C' — binary header CRC32

	// Tipi frame
	ZRQINIT    byte = 0  // Request receive init
	ZRINIT     byte = 1  // Receive init
	ZSINIT     byte = 2  // Send init sequence
	ZACK       byte = 3  // ACK
	ZFILE      byte = 4  // File name/info
	ZSKIP      byte = 5  // Skip file
	ZNAK       byte = 6  // NAK (error)
	ZABORT     byte = 7  // Abort
	ZFIN       byte = 8  // Finish session
	ZRPOS      byte = 9  // Resume data from position
	ZDATA      byte = 10 // Data packet(s) follow
	ZEOF       byte = 11 // End of file
	ZFERR      byte = 12 // Fatal read/write error
	ZCRC       byte = 13 // Request file CRC
	ZCHALLENGE byte = 14
	ZCOMPL     byte = 15 // Request complete
	ZCAN       byte = 16 // CAN chars received, abort

	// Subpacket end types
	ZCRCE byte = 0x68 // 'h' — CRC next, frame ends
	ZCRCG byte = 0x69 // 'i' — CRC next, frame continues
	ZCRCQ byte = 0x6A // 'j' — CRC next, frame continues, ZACK expected
	ZCRCW byte = 0x6B // 'k' — CRC next, frame ends, ZACK expected

	// ZRINIT capability flags
	CANFDX  byte = 0x01 // Full duplex
	CANOVIO byte = 0x02 // Overlay I/O
	CANBRK  byte = 0x04 // Send break
	CANFC32 byte = 0x20 // CRC-32

	// Limiti
	MaxFileSize  = 4 * 1024 * 1024 * 1024 // 4 GB
	MaxBufSize   = 64 * 1024              // 64 KB — limite buffer receiver/sender (PT-002: anti-OOM)
	BlockSize    = 1024
	MaxRetries   = 5
)

// Bytes che devono essere escaped con ZDLE.
// Include 0xFF (Telnet IAC) — critico per connessioni Telnet.
var zdleEscapeSet = map[byte]bool{
	0x18: true, 0x10: true, 0x11: true, 0x13: true,
	0x90: true, 0x91: true, 0x93: true, 0x0D: true,
	0x8D: true, 0xFF: true,
}

// AbortSeq è la sequenza di abort: 8 CAN + 8 BS
var AbortSeq = []byte{
	0x18, 0x18, 0x18, 0x18, 0x18, 0x18, 0x18, 0x18,
	0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08, 0x08,
}

// ZRQINITHex è il pattern di rilevamento ZMODEM
var ZRQINITHex = []byte("**\x18B00")

// FrameNames mappa i tipi frame ai nomi leggibili
var FrameNames = map[byte]string{
	0: "ZRQINIT", 1: "ZRINIT", 2: "ZSINIT", 3: "ZACK",
	4: "ZFILE", 5: "ZSKIP", 6: "ZNAK", 7: "ZABORT",
	8: "ZFIN", 9: "ZRPOS", 10: "ZDATA", 11: "ZEOF", 16: "ZCAN",
}

// ─────────────────────────────────────────────
// Tabelle CRC precalcolate
// ─────────────────────────────────────────────

var crc16Table [256]uint16
var crc32Table [256]uint32

func init() {
	// CRC16 CCITT (polinomio 0x1021)
	for i := 0; i < 256; i++ {
		crc := uint16(i) << 8
		for j := 0; j < 8; j++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
		crc16Table[i] = crc
	}

	// CRC32 (polinomio 0xEDB88320)
	for i := 0; i < 256; i++ {
		crc := uint32(i)
		for j := 0; j < 8; j++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xEDB88320
			} else {
				crc >>= 1
			}
		}
		crc32Table[i] = crc
	}
}

// CRC16 calcola CRC16 CCITT.
func CRC16(data []byte, initial uint16) uint16 {
	crc := initial
	for _, b := range data {
		crc = (crc << 8) ^ crc16Table[((crc>>8)^uint16(b))&0xFF]
	}
	return crc
}

// CRC32 calcola CRC32.
func CRC32(data []byte, initial uint32) uint32 {
	crc := initial
	for _, b := range data {
		crc = crc32Table[(crc^uint32(b))&0xFF] ^ (crc >> 8)
	}
	return crc ^ 0xFFFFFFFF
}

// ─────────────────────────────────────────────
// ZDLE Escaping
// ─────────────────────────────────────────────

// ZDLEEscape applica ZDLE escaping ai bytes che lo richiedono.
func ZDLEEscape(data []byte) []byte {
	out := make([]byte, 0, len(data)*2)
	for _, b := range data {
		if zdleEscapeSet[b] || b == ZDLE {
			out = append(out, ZDLE, b^0x40)
		} else {
			out = append(out, b)
		}
	}
	return out
}

// ─────────────────────────────────────────────
// Costruzione header
// ─────────────────────────────────────────────

func hexByte(b byte) []byte {
	return []byte(fmt.Sprintf("%02x", b))
}

// BuildHexHeader costruisce un header ZMODEM in formato hex.
func BuildHexHeader(frameType, p0, p1, p2, p3 byte) []byte {
	hdr := []byte{frameType, p0, p1, p2, p3}
	crcVal := CRC16(hdr, 0)

	out := make([]byte, 0, 32)
	out = append(out, ZPAD, ZPAD, ZDLE, ZHEX)
	for _, b := range hdr {
		out = append(out, hexByte(b)...)
	}
	out = append(out, hexByte(byte(crcVal>>8))...)
	out = append(out, hexByte(byte(crcVal&0xFF))...)
	out = append(out, '\r', '\n')
	return out
}

// BuildBinHeader costruisce un header ZMODEM in formato binario.
func BuildBinHeader(frameType, p0, p1, p2, p3 byte, useCRC32 bool) []byte {
	hdr := []byte{frameType, p0, p1, p2, p3}

	out := make([]byte, 0, 32)
	out = append(out, ZPAD, ZDLE)

	if useCRC32 {
		out = append(out, ZBIN32)
		crcVal := CRC32(hdr, 0xFFFFFFFF)
		out = append(out, ZDLEEscape(hdr)...)
		crcBytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(crcBytes, crcVal)
		out = append(out, ZDLEEscape(crcBytes)...)
	} else {
		out = append(out, ZBIN)
		crcVal := CRC16(hdr, 0)
		out = append(out, ZDLEEscape(hdr)...)
		crcBytes := make([]byte, 2)
		binary.BigEndian.PutUint16(crcBytes, crcVal)
		out = append(out, ZDLEEscape(crcBytes)...)
	}
	return out
}

// BuildPosHeader costruisce un header hex con posizione a 32 bit (little-endian).
func BuildPosHeader(frameType byte, position uint32) []byte {
	p0 := byte(position & 0xFF)
	p1 := byte((position >> 8) & 0xFF)
	p2 := byte((position >> 16) & 0xFF)
	p3 := byte((position >> 24) & 0xFF)
	return BuildHexHeader(frameType, p0, p1, p2, p3)
}

// BuildBinPosHeader costruisce un header binario con posizione a 32 bit.
func BuildBinPosHeader(frameType byte, position uint32, useCRC32 bool) []byte {
	p0 := byte(position & 0xFF)
	p1 := byte((position >> 8) & 0xFF)
	p2 := byte((position >> 16) & 0xFF)
	p3 := byte((position >> 24) & 0xFF)
	return BuildBinHeader(frameType, p0, p1, p2, p3, useCRC32)
}

// ─────────────────────────────────────────────
// Costruzione subpacket dati
// ─────────────────────────────────────────────

// BuildDataSubpacket costruisce un subpacket di dati ZMODEM.
func BuildDataSubpacket(data []byte, endType byte, useCRC32 bool) []byte {
	out := make([]byte, 0, len(data)*2+16)
	out = append(out, ZDLEEscape(data)...)
	out = append(out, ZDLE, endType)

	checkData := make([]byte, len(data)+1)
	copy(checkData, data)
	checkData[len(data)] = endType

	if useCRC32 {
		crcVal := CRC32(checkData, 0xFFFFFFFF)
		crcBytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(crcBytes, crcVal)
		out = append(out, ZDLEEscape(crcBytes)...)
	} else {
		crcVal := CRC16(checkData, 0)
		crcBytes := make([]byte, 2)
		binary.BigEndian.PutUint16(crcBytes, crcVal)
		out = append(out, ZDLEEscape(crcBytes)...)
	}
	return out
}

// ─────────────────────────────────────────────
// Parsing header
// ─────────────────────────────────────────────

// HexHeader contiene il risultato del parsing di un header hex
type HexHeader struct {
	FrameType byte
	P0, P1, P2, P3 byte
	Consumed  int
}

// BinHeader contiene il risultato del parsing di un header binario
type BinHeader struct {
	FrameType byte
	P0, P1, P2, P3 byte
	Consumed  int
	IsCRC32   bool
}

// DataSubpacket contiene il risultato del parsing di un subpacket dati
type DataSubpacket struct {
	Payload  []byte
	EndType  byte
	Consumed int
}

func hexVal(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

// ParseHexHeader prova a parsare un header hex ZMODEM dai dati.
func ParseHexHeader(data []byte) *HexHeader {
	n := len(data)
	idx := 0

	// Cerca il pattern ** ZDLE ZHEX
	found := false
	for idx < n-3 {
		if data[idx] == ZPAD && data[idx+1] == ZPAD &&
			data[idx+2] == ZDLE && data[idx+3] == ZHEX {
			found = true
			break
		}
		idx++
	}
	if !found {
		return nil
	}

	idx += 4 // dopo **\x18B

	// Servono 14 hex chars (type=2, p0-p3=8, crc=4)
	if idx+14 > n {
		return nil
	}

	hexChars := data[idx : idx+14]
	idx += 14

	// Decodifica hex → 7 bytes
	vals := make([]byte, 7)
	for i := 0; i < 7; i++ {
		vals[i] = (hexVal(hexChars[i*2]) << 4) | hexVal(hexChars[i*2+1])
	}

	frameType := vals[0]
	p0, p1, p2, p3 := vals[1], vals[2], vals[3], vals[4]
	crcRecv := (uint16(vals[5]) << 8) | uint16(vals[6])

	// Verifica CRC
	hdrBytes := []byte{frameType, p0, p1, p2, p3}
	crcCalc := CRC16(hdrBytes, 0)
	if crcRecv != crcCalc {
		return nil
	}

	// Salta CR LF XON e 0x8A opzionali
	for idx < n && (data[idx] == 0x0D || data[idx] == 0x0A ||
		data[idx] == 0x11 || data[idx] == 0x8A) {
		idx++
	}

	return &HexHeader{
		FrameType: frameType,
		P0: p0, P1: p1, P2: p2, P3: p3,
		Consumed: idx,
	}
}

// ParseBinHeader prova a parsare un header binario ZMODEM.
func ParseBinHeader(data []byte) *BinHeader {
	n := len(data)
	idx := 0

	// Cerca pattern ZPAD ZDLE ZBIN/ZBIN32
	found := false
	for idx < n-2 {
		if data[idx] == ZPAD && data[idx+1] == ZDLE &&
			(data[idx+2] == ZBIN || data[idx+2] == ZBIN32) {
			found = true
			break
		}
		idx++
	}
	if !found {
		return nil
	}

	isCRC32 := data[idx+2] == ZBIN32
	idx += 3

	// Unescape header (5 bytes: type + p0-p3)
	hdr := make([]byte, 0, 5)
	for len(hdr) < 5 && idx < n {
		if data[idx] == ZDLE {
			idx++
			if idx < n {
				hdr = append(hdr, data[idx]^0x40)
			}
		} else {
			hdr = append(hdr, data[idx])
		}
		idx++
	}
	if len(hdr) < 5 {
		return nil
	}

	// Unescape CRC
	crcLen := 2
	if isCRC32 {
		crcLen = 4
	}
	crcBytes := make([]byte, 0, crcLen)
	for len(crcBytes) < crcLen && idx < n {
		if data[idx] == ZDLE {
			idx++
			if idx < n {
				crcBytes = append(crcBytes, data[idx]^0x40)
			}
		} else {
			crcBytes = append(crcBytes, data[idx])
		}
		idx++
	}
	if len(crcBytes) < crcLen {
		return nil
	}

	// Verifica CRC
	if isCRC32 {
		crcRecv := binary.LittleEndian.Uint32(crcBytes)
		crcCalc := CRC32(hdr, 0xFFFFFFFF)
		if crcRecv != crcCalc {
			return nil
		}
	} else {
		crcRecv := binary.BigEndian.Uint16(crcBytes)
		crcCalc := CRC16(hdr, 0)
		if crcRecv != crcCalc {
			return nil
		}
	}

	return &BinHeader{
		FrameType: hdr[0],
		P0: hdr[1], P1: hdr[2], P2: hdr[3], P3: hdr[4],
		Consumed: idx,
		IsCRC32:  isCRC32,
	}
}

// ─────────────────────────────────────────────
// Parsing subpacket dati
// ─────────────────────────────────────────────

// ParseDataSubpacket parsa un subpacket dati ZMODEM dal buffer.
func ParseDataSubpacket(data []byte, useCRC32 bool) *DataSubpacket {
	payload := make([]byte, 0, len(data))
	idx := 0
	n := len(data)
	var endType byte
	foundEnd := false

	for idx < n {
		b := data[idx]
		if b == ZDLE {
			idx++
			if idx >= n {
				return nil // incompleto
			}
			nb := data[idx]
			if nb == ZCRCE || nb == ZCRCG || nb == ZCRCQ || nb == ZCRCW {
				endType = nb
				idx++
				foundEnd = true
				break
			}
			payload = append(payload, nb^0x40)
		} else {
			payload = append(payload, b)
		}
		idx++
	}

	if !foundEnd {
		return nil
	}

	// Leggi CRC
	crcLen := 2
	if useCRC32 {
		crcLen = 4
	}
	crcBytes := make([]byte, 0, crcLen)
	for len(crcBytes) < crcLen && idx < n {
		if data[idx] == ZDLE {
			idx++
			if idx < n {
				crcBytes = append(crcBytes, data[idx]^0x40)
			}
		} else {
			crcBytes = append(crcBytes, data[idx])
		}
		idx++
	}

	if len(crcBytes) < crcLen {
		return nil
	}

	// Verifica CRC
	checkData := make([]byte, len(payload)+1)
	copy(checkData, payload)
	checkData[len(payload)] = endType

	if useCRC32 {
		crcRecv := binary.LittleEndian.Uint32(crcBytes)
		crcCalc := CRC32(checkData, 0xFFFFFFFF)
		if crcRecv != crcCalc {
			return nil
		}
	} else {
		crcRecv := binary.BigEndian.Uint16(crcBytes)
		crcCalc := CRC16(checkData, 0)
		if crcRecv != crcCalc {
			return nil
		}
	}

	return &DataSubpacket{
		Payload:  payload,
		EndType:  endType,
		Consumed: idx,
	}
}

// ─────────────────────────────────────────────
// Helper: posizione da parametri header
// ─────────────────────────────────────────────

// PositionFromParams converte p0-p3 in posizione uint32 (little-endian).
func PositionFromParams(p0, p1, p2, p3 byte) uint32 {
	return uint32(p0) | uint32(p1)<<8 | uint32(p2)<<16 | uint32(p3)<<24
}

// Detect controlla se i dati contengono un inizio ZMODEM (ZRQINIT).
func Detect(data []byte) bool {
	return containsBytes(data, ZRQINITHex) ||
		containsBytes(data, []byte{0x2A, 0x18, 0x41, 0x00}) ||
		containsBytes(data, []byte{0x2A, 0x18, 0x43, 0x00})
}

func containsBytes(data, pattern []byte) bool {
	if len(pattern) > len(data) {
		return false
	}
	for i := 0; i <= len(data)-len(pattern); i++ {
		match := true
		for j := 0; j < len(pattern); j++ {
			if data[i+j] != pattern[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
