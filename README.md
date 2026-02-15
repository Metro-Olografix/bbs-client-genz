# BBS Client for Gen-Z

Un client BBS telnet moderno con GUI nativa, scritto in Go con [Wails v2](https://wails.io). Pensato per chi vuole esplorare le BBS ancora attive nel 2026 con un'interfaccia che ricorda i terminali degli anni '90, ma gira su macOS, Windows e Linux come app nativa.

![Go](https://img.shields.io/badge/Go-1.22-00ADD8?logo=go&logoColor=white)
![Wails](https://img.shields.io/badge/Wails-v2-FF4444?logo=data:image/svg+xml;base64,...)
![Platform](https://img.shields.io/badge/Platform-macOS%20%7C%20Windows%20%7C%20Linux-lightgrey)
![License](https://img.shields.io/badge/License-MIT-green)

## Funzionalità

- **Terminale ANSI completo** — rendering via canvas HTML5 con supporto colori 16/256, bold, underline, blink e tutti i codici escape ANSI/VT100
- **Font IBM VGA autentico** — il font Px437 IBM VGA 8×16 per l'aspetto DOS originale, con fallback su VT323
- **ZMODEM** — download e upload file integrato, con progress bar, velocità e ETA in tempo reale
- **CRT Shader** — effetto monitor vintage con scanlines, phosphor glow, vignette, sub-pixel RGB e animazione di accensione
- **Log sessione** — registrazione automatica di ogni sessione con viewer integrato per rileggere le sessioni passate
- **Lista BBS precaricata** — dropdown con BBS attive (Metro Olografix, Level 29, Cyberia, ecc.)
- **Cross-platform** — build native per macOS (.app + DMG), Windows (.exe) e Linux

## Screenshot

> TODO: aggiungere screenshot del terminale con CRT shader attivo

## Requisiti per la build

- [Go](https://go.dev/dl/) >= 1.22
- [Wails CLI v2](https://wails.io/docs/gettingstarted/installation) — `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
- Per la build Windows da macOS: `brew install mingw-w64`
- Per la build Linux (nativa): `sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev`

## Build

```bash
# Solo macOS
./scripts/build-release.sh 1.1.0 macos

# Solo Windows (cross-compile da macOS)
./scripts/build-release.sh 1.1.0 windows

# Solo Linux (richiede macchina Linux nativa)
./scripts/build-release.sh 1.1.0 linux

# Tutte le piattaforme
./scripts/build-release.sh 1.1.0 all
```

La build Linux non supporta cross-compilazione: va eseguita su una macchina Linux con le dipendenze GTK/WebKit installate. Lo script lo rileva automaticamente e mostra le istruzioni se lanciato da macOS.

I file di release vengono generati in `dist/`:

| File | Piattaforma |
|------|-------------|
| `BBS-Client-v1.1.0-macOS.dmg` | macOS (DMG con drag-to-Applications) |
| `BBS-Client-v1.1.0-macOS.zip` | macOS (ZIP) |
| `BBS-Client-v1.1.0-Windows-x64.zip` | Windows x64 |
| `BBS-Client-v1.1.0-Linux-x64.tar.gz` | Linux x64 |

## Sviluppo

```bash
# Clone
git clone https://github.com/UTENTE/bbs-client-go.git
cd bbs-client-go

# Dev mode con hot reload
wails dev

# Build di produzione
wails build
```

## Architettura

```
bbs-client-go/
├── app.go                  # Backend Wails — logica principale, IPC
├── main_gui.go             # Entry point GUI
├── cmd/bbsclient/main.go   # Entry point CLI (telnet puro)
├── internal/
│   ├── ansi/screen.go      # Parser ANSI/VT100, screen buffer 80×25
│   ├── telnet/telnet.go    # Client telnet con negoziazione IAC
│   └── zmodem/
│       ├── protocol.go     # Costanti e funzioni ZMODEM
│       ├── receiver.go     # Download ZMODEM
│       └── sender.go       # Upload ZMODEM
├── frontend/
│   ├── index.html          # Layout UI
│   ├── src/main.js         # Renderer canvas, keyboard, eventi
│   ├── src/style.css       # Stile Telix + CRT shader
│   └── fonts/              # IBM VGA font
├── scripts/
│   └── build-release.sh    # Script build multi-piattaforma
└── wails.json              # Configurazione Wails
```

## Scorciatoie da tastiera

| Tasto | Azione |
|-------|--------|
| **F1** | Mostra/chiudi help |
| **Cmd+D** | Disconnetti dalla BBS |
| **F2—F12** | Tasti funzione BBS |
| **Ctrl+A—Z** | Sequenze di controllo |
| **Spazio / →** | Pagina avanti (log viewer) |
| **←** | Pagina indietro (log viewer) |
| **ESC** | Esci dal log viewer |

## Release

| Versione | Note |
|----------|------|
| **v1.1.0** | Code review: 19 bug fix (sicurezza, stabilità, race condition). CRT shader. Help overlay (F1). |
| **v1.0.0** | Prima release stabile |
| **v0.9.1** | Fix rendering terminale, font IBM VGA, log viewer |
| **v0.9.0** | Prima release pubblica |

## Crediti

Creato da **Stefano "NeURo" Chiccarelli**

Vibe codato con [Cowork](https://claude.ai) — dedicato alla Metro Olografix e a tutti i Gen-Z.

## Licenza

MIT
