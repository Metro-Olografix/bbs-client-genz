# BBS Client for Gen-Z v1.0.0 — Code Review & Security Assessment

**Data:** 15 febbraio 2026
**Revisore:** Claude Opus 4.6
**Committente:** RJ45LAB

---

## PARTE 1 — Bug Funzionali e Logici

### BUG-001: Race condition su `connected` in `app.go` [MEDIA]

Il flag `a.connected` viene impostato a `true` nella goroutine `eventLoop()` (riga 510) quando riceve `EventConnected`, ma anche letto da `Connect()` (riga 175) e `Disconnect()` (riga 204) **senza acquisire il mutex**. Solo `GetScreen()` e le funzioni log usano `a.mu`.

**Impatto:** In condizioni di timing sfavorevole, `SendKey()` potrebbe inviare dati prima che lo stato sia consistente, oppure `Disconnect()` potrebbe essere chiamato quando `connected` non è ancora `true`.

**Fix suggerito:** Proteggere tutti gli accessi a `a.connected` con `a.mu.Lock()`.

---

### BUG-002: `eventLoop()` non termina mai [BASSA]

La goroutine `eventLoop()` (riga 493) esegue un `for { select { ... } }` infinito. Quando l'app viene chiusa via Wails, la goroutine rimane in vita perché nessun channel viene chiuso.

**Impatto:** Memory leak trascurabile per un'app desktop, ma non è Go idiomatico.

**Fix suggerito:** Aggiungere un `case <-a.ctx.Done(): return` nel select.

---

### BUG-003: Drop silenzioso dati su `DataCh` pieno [MEDIA]

In `telnet.go` riga 320-326, `emitData()` usa un `select` con `default` — se il channel è pieno (cap=64), i dati vengono **scartati silenziosamente**. Lo stesso per `emitEvent()` (riga 329-333).

**Impatto:** Durante burst di dati (es. download ANSI art pesante), pacchetti possono andare persi causando corruzione grafica nello schermo.

**Fix suggerito:** Aumentare il buffer del channel o usare un meccanismo di coalescing.

---

### BUG-004: `processTelnet` perde dati su IAC incompleto [BASSA]

In `telnet.go` riga 431, se `b == IAC` e `i+1 >= n` (IAC all'ultimo byte del buffer), il ciclo fa `break` e l'ultimo byte viene perso. Lo stesso vale per `DO/DONT/WILL/WONT` incompleti (riga 443) e `SB` incompleta (riga 453).

**Impatto:** In rari casi, specialmente con connessioni lente, una sequenza IAC a cavallo di due recv potrebbe essere persa.

**Fix suggerito:** Salvare i byte rimanenti in un buffer di riporto per il prossimo ciclo di ricezione.

---

### BUG-005: Sender riapre il file ad ogni `ZRPOS` senza chiudere il precedente [MEDIA]

In `sender.go` riga 250, `startSending()` apre il file con `os.Open()` ma non controlla se `s.fileHandle` è già aperto. Se il server invia più ZRPOS (retry), il file handle precedente resta aperto.

**Impatto:** File handle leak; su Windows potrebbe impedire altre operazioni sul file.

**Fix suggerito:** Aggiungere `s.cleanup()` all'inizio di `startSending()`.

---

### BUG-006: Padding status bar con `len()` su stringa UTF-8 [BASSA]

In `app.go` riga 476, `for len(bar) < 80` usa `len()` che conta i byte, non i caratteri. La stringa `bar` contiene frecce Unicode (▶, ←, ✖) che occupano più di 1 byte.

**Impatto:** La barra di navigazione del log viewer potrebbe essere più corta di 80 colonne visive, o leggermente più lunga.

**Fix suggerito:** Usare `utf8.RuneCountInString()` o equivalente.

---

### BUG-007: `Connect()` non resetta lo screen prima di una nuova connessione [BASSA]

Quando ci si connette a una nuova BBS, lo schermo mantiene il contenuto della sessione precedente finché la BBS non invia un clear screen.

**Fix suggerito:** Aggiungere `a.screen.Reset()` in `Connect()` prima di connettersi.

---

### BUG-008: Ctrl+] non disconnette nel frontend [BASSA]

Il messaggio splash dice "Ctrl+] per disconnettere" (main.js riga 580), ma nel keyboard handler non c'è nessuna gestione di Ctrl+]. Il `SendCtrlKey` invierebbe `0x1D` (GS) al server.

**Fix suggerito:** Aggiungere un handler specifico in `setupKeyboard()` per Ctrl+] che chiami `Disconnect()`.

---

### BUG-009: ZMODEM cancel button non annulla realmente il trasferimento [MEDIA]

In `main.js` riga 352, il click su `btn-zmodem-cancel` nasconde solo l'overlay ma **non chiama** `CancelZmodem()` sul backend. Il trasferimento ZMODEM continua silenziosamente.

**Fix suggerito:** Aggiungere `await window.go.main.App.CancelZmodem()` nel handler, e esporre `CancelZmodem` come metodo Wails in `app.go`.

---

### BUG-010: `getScreen()` chiamata doppia (serializzazione JSON pesante) [BASSA]

In `updateScreen()` (main.js riga 181-182), `GetScreen()` e `GetCursor()` sono due chiamate separate al backend, ognuna con serializzazione JSON. `GetScreen()` serializza 80×25×11 campi = ~22.000 campi JSON ogni frame.

**Fix suggerito:** Unificare in una singola chiamata `GetScreenAndCursor()` per ridurre l'overhead IPC.

---

## PARTE 2 — Review di Sicurezza

### SEC-001: Path traversal nel receiver ZMODEM — PROTETTO ✓

Il receiver (`receiver.go` righe 322-341) implementa correttamente:
- Sostituzione backslash → forward slash
- `filepath.Base()` per estrarre solo il nome file
- Regex sanitization `[^a-zA-Z0-9._\-]`
- Rifiuto di `.`, `..`, file che iniziano con `.`
- Verifica `filepath.Abs()` con prefix check

**Valutazione:** La protezione è solida. Nessuna vulnerabilità trovata.

---

### SEC-002: Assenza di TLS — connessione in chiaro [ALTA]

Tutte le connessioni Telnet avvengono su TCP in chiaro (porta 23). Credenziali utente, messaggi e file transitano in plaintext.

**Impatto:** Sniffing di rete triviale su LAN/WiFi. Le credenziali BBS vengono catturate facilmente.

**Nota:** Questo è intrinseco al protocollo Telnet e alla natura delle BBS. Non è un bug dell'applicazione, ma una limitazione architetturale del protocollo. La maggior parte delle BBS non supporta SSH o TLS.

**Mitigazione possibile:** Supportare connessioni SSH (porta 22) come alternativa, usando `golang.org/x/crypto/ssh`.

---

### SEC-003: Escape di byte Telnet IAC in ZMODEM — PROTETTO ✓

Il set `zdleEscapeSet` in `protocol.go` riga 76 include correttamente `0xFF` (Telnet IAC), evitando che byte `0xFF` nei dati ZMODEM vengano interpretati come comandi Telnet.

---

### SEC-004: Buffer overflow nel parser CSI — PROTETTO ✓

`screen.go` riga 282 limita il buffer CSI a `MaxCSIBuf = 1024` caratteri, prevenendo OOM da sequenze CSI malformate inviate da un server malevolo.

---

### SEC-005: Permessi file troppo ampi per logs e downloads [BASSA]

`os.MkdirAll(dir, 0755)` crea directory con permessi world-readable. I log di sessione contengono tutto il traffico (incluse eventuali credenziali digitate).

**Fix suggerito:** Usare `0700` per le directory `logs/` e `downloads/`.

---

### SEC-006: Log di sessione contiene credenziali in chiaro [MEDIA]

La funzione `writeSessionLog()` (app.go riga 152) scrive tutto il testo decodificato nel log, inclusi caratteri digitati dall'utente che tipicamente includono username e password della BBS.

**Impatto:** Se un attaccante accede ai file di log, ottiene le credenziali BBS.

**Mitigazione:** Documentare chiaramente che i log contengono dati sensibili. Valutare un'opzione per disabilitare il logging.

---

### SEC-007: Assenza di validazione input su host/porta dal frontend [BASSA]

`Connect()` accetta qualsiasi stringa come `host` e qualsiasi `port > 0`. Non c'è validazione DNS, blacklist di IP interni, o limiti di porta.

**Impatto:** In un contesto desktop standalone questo è accettabile. Sarebbe un problema se l'app fosse web-based (SSRF).

---

### SEC-008: Nessun limite dimensione file ZMODEM upload [BASSA]

Il sender (`sender.go`) non controlla la dimensione del file prima dell'upload. Per il download, il receiver ha `MaxFileSize = 4GB`.

**Fix suggerito:** Aggiungere un check `MaxFileSize` anche nel sender.

---

### SEC-009: `OnResponse` (DSR) può essere sfruttato per iniezione [MEDIA]

Il callback `OnResponse` in `screen.go` invia la posizione del cursore al server quando riceve DSR (Device Status Report, ESC[6n). Un server malevolo potrebbe:
1. Posizionare il cursore a coordinate specifiche
2. Inviare DSR
3. Ricevere la risposta con la posizione

Questo è il comportamento standard del terminale, ma combinato con sequenze ANSI creative, un server malevolo potrebbe costruire un "oracle" per rilevare lo stato del terminale.

**Impatto:** Basso — informazione limitata alla posizione del cursore.

---

### SEC-010: BBS list caricata da disco senza verifica integrità [BASSA]

`loadBBSFromDisk()` cerca file `short_*.txt` vicino all'eseguibile. Un attaccante con accesso al filesystem potrebbe sostituire il file con una lista contenente host malevoli.

**Impatto:** Basso — l'utente deve comunque cliccare su Connetti, e l'host è visibile nella toolbar.

---

## PARTE 3 — Penetration Test

### Threat Model

L'applicazione è un client desktop che si connette a server BBS remoti via Telnet. Le superfici di attacco principali sono:

1. **Server BBS malevolo** → invia dati crafted al client
2. **Man-in-the-Middle** → intercetta/modifica traffico Telnet
3. **Accesso locale** → file di log, configurazione

### PENTEST-001: Fuzzing del parser ANSI — Injection via server malevolo [MEDIA]

**Vettore:** Un server BBS malevolo invia sequenze ANSI crafted per crashare il client.

**Test effettuato:** Analisi statica del parser (`screen.go`).

**Risultati:**
- Il parser gestisce correttamente i caratteri di controllo (0x00-0x1F)
- Il buffer CSI è limitato a 1024 byte (protezione OOM)
- `parseParams()` gestisce parametri non validi con fallback a default
- `putChar()` controlla `CursorX >= Cols` e fa line wrap
- `lineFeed()` gestisce correttamente lo scroll quando `CursorY >= Rows-1`
- I comandi sconosciuti in stato ESC e CSI tornano a `stateNormal`

**Vulnerabilità trovata:** Nessun crash possibile tramite sequenze ANSI malformate. Il parser è robusto.

**Nota:** L'assenza di gestione dello stato OSC (riga 294-298) semplicemente ignora tutto fino a BEL o ESC, il che è sicuro.

---

### PENTEST-002: ZMODEM protocol fuzzing [MEDIA]

**Vettore:** Un server invia pacchetti ZMODEM malformati per sfruttare il receiver.

**Test effettuato:** Analisi statica di `receiver.go` e `protocol.go`.

**Risultati:**
- `processBuffer()` ha un limite di 200 iterazioni (protezione loop infinito)
- `ParseHexHeader()` verifica CRC16 — header corrotti vengono scartati
- `ParseBinHeader()` verifica CRC16 o CRC32
- `ParseDataSubpacket()` verifica CRC su ogni subpacket
- `tryParseHeader()` scarta buffer > 1024 byte cercando il prossimo ZPAD
- La dimensione file è limitata a 4GB (`MaxFileSize`)
- Il filename è sanitizzato con regex e path traversal check

**Vulnerabilità trovata:**
- Il buffer `r.buf` in `receiver.go` cresce senza limite se il server invia dati senza header validi e senza ZPAD. Il check `> 1024` in `tryParseHeader()` riga 156 lo mitiga, ma solo se viene chiamato in quello stato.
- **Impatto:** Potenziale OOM se un server malevolo invia megabyte di dati non-ZMODEM dopo aver triggerato il detect.

**Fix suggerito:** Aggiungere un limite assoluto al buffer (es. 64KB) oltre il quale il receiver viene cancellato.

---

### PENTEST-003: Telnet IAC injection dentro stream ZMODEM [BASSA]

**Vettore:** Un MitM inietta sequenze IAC nel flusso TCP per confondere il client.

**Test:** Il flusso TCP viene processato da `processTelnet()` che rimuove/gestisce tutte le sequenze IAC prima di passare i dati a ZMODEM. Lo ZDLE escaping include `0xFF`.

**Risultato:** Il client è protetto. Le sequenze IAC nei dati ZMODEM sono correttamente escaped e processate.

---

### PENTEST-004: Denial of Service locale via log flooding [BASSA]

**Vettore:** Una sessione BBS molto lunga genera un file di log enorme.

**Test:** `writeSessionLog()` scrive tutto il traffico senza limiti.

**Risultato:** Nessun limite sulla dimensione del log. Una sessione con download pesanti o ANSI art ciclica potrebbe generare log di centinaia di MB.

**Fix suggerito:** Aggiungere rotazione dei log o un limite massimo (es. 50MB per file).

---

### PENTEST-005: ZMODEM auto-detect false positive [BASSA]

**Vettore:** Un server BBS invia dati normali che contengono per coincidenza il pattern `**\x18B00` (o le varianti binarie).

**Test:** `Detect()` in `protocol.go` cerca pattern specifici nel buffer.

**Risultato:** Un falso positivo attiverebbe il receiver ZMODEM, che invierebbe un ZRINIT al server. Se il server non risponde con ZFILE, il receiver resterebbe in stato `RxWaitZFile` indefinitamente.

**Fix suggerito:** Aggiungere un timeout globale (es. 30s) per il receiver che entra in stato init senza ricevere ZFILE.

---

### PENTEST-006: Nessuna protezione contro clickjacking / UI redressing [N/A]

**Risultato:** Non applicabile — è un'app desktop Wails, non un'applicazione web. Il frontend è servito localmente.

---

### PENTEST-007: Verifica Cross-recv ZMODEM detect buffer [BASSA]

**Vettore:** Il pattern ZMODEM potrebbe essere diviso tra due `recv()` successive.

**Test:** Il `zmodemDetectBuf` (telnet.go righe 295-312) mantiene gli ultimi 64 byte tra cicli di ricezione.

**Risultato:** La protezione è corretta per pattern fino a 64 byte. I pattern ZMODEM sono tutti < 10 byte, quindi 64 byte è sufficiente.

---

## Riepilogo

| ID | Tipo | Severità | Stato |
|----|------|----------|-------|
| BUG-001 | Race condition `connected` | MEDIA | Da fixare |
| BUG-002 | Goroutine leak `eventLoop` | BASSA | Consigliato |
| BUG-003 | Drop dati su channel pieno | MEDIA | Da fixare |
| BUG-004 | IAC incompleto perso | BASSA | Consigliato |
| BUG-005 | File handle leak sender | MEDIA | Da fixare |
| BUG-006 | Padding UTF-8 status bar | BASSA | Cosmetico |
| BUG-007 | Screen non resettato su riconnessione | BASSA | Consigliato |
| BUG-008 | Ctrl+] non implementato | BASSA | Da fixare |
| BUG-009 | ZMODEM cancel non funziona | MEDIA | Da fixare |
| BUG-010 | Doppia chiamata IPC screen | BASSA | Ottimizzazione |
| SEC-001 | Path traversal ZMODEM | — | ✓ Protetto |
| SEC-002 | Connessione in chiaro | ALTA | Intrinseco Telnet |
| SEC-003 | IAC escape ZMODEM | — | ✓ Protetto |
| SEC-004 | Buffer overflow CSI | — | ✓ Protetto |
| SEC-005 | Permessi file 0755 | BASSA | Da fixare |
| SEC-006 | Credenziali nei log | MEDIA | Documentare |
| SEC-007 | No validazione host/porta | BASSA | Accettabile |
| SEC-008 | No limite upload ZMODEM | BASSA | Consigliato |
| SEC-009 | DSR oracle | MEDIA | Informativo |
| SEC-010 | BBS list non verificata | BASSA | Accettabile |
| PT-001 | Fuzzing parser ANSI | — | ✓ Robusto |
| PT-002 | ZMODEM buffer OOM | MEDIA | Da fixare |
| PT-003 | IAC injection ZMODEM | — | ✓ Protetto |
| PT-004 | Log flooding DoS | BASSA | Consigliato |
| PT-005 | ZMODEM false positive | BASSA | Consigliato |
| PT-007 | Cross-recv detect | — | ✓ Corretto |

### Priorità di intervento consigliate:

**Priorità 1 (fix immediato):**
- BUG-009: ZMODEM cancel non funziona (bug visibile all'utente)
- BUG-005: File handle leak sender
- BUG-001: Race condition su `connected`

**Priorità 2 (prossima release):**
- BUG-003: Drop dati su channel pieno
- PT-002: Limite buffer ZMODEM receiver
- SEC-005: Permessi directory 0700
- BUG-008: Implementare Ctrl+]

**Priorità 3 (miglioramenti):**
- BUG-002, BUG-004, BUG-006, BUG-007, BUG-010
- SEC-006, SEC-008, PT-004, PT-005
