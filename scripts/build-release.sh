#!/bin/bash
# ─────────────────────────────────────────────────
# build-release.sh — Crea release distribuibili
# Uso: ./scripts/build-release.sh [versione] [piattaforma]
# Es:  ./scripts/build-release.sh 1.1.0
#      ./scripts/build-release.sh 1.1.0 macos
#      ./scripts/build-release.sh 1.1.0 windows
#      ./scripts/build-release.sh 1.1.0 linux
#      ./scripts/build-release.sh 1.1.0 all
# Piattaforme: macos, windows, linux, all (default: macos)
# Nota: linux richiede di essere eseguito su una macchina Linux
#       con le dipendenze GTK/WebKit installate.
# ─────────────────────────────────────────────────

set -e

VERSION="${1:-1.1.0}"
PLATFORM="${2:-macos}"
APP_NAME="BBS Client for Gen-Z"
PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
DIST_DIR="$PROJECT_DIR/dist"

echo "══════════════════════════════════════════"
echo "  Building ${APP_NAME} v${VERSION}"
echo "  Platform: ${PLATFORM}"
echo "══════════════════════════════════════════"

# Pulizia
rm -rf "$PROJECT_DIR/build/bin"
mkdir -p "$DIST_DIR"
cd "$PROJECT_DIR"

# ─── macOS ───
build_macos() {
    echo ""
    echo "── macOS Build ──────────────────────────"
    APP_PATH="$PROJECT_DIR/build/bin/${APP_NAME}.app"

    wails build

    if [ ! -d "$APP_PATH" ]; then
        echo "✗ Errore: ${APP_PATH} non trovato!"
        return 1
    fi

    # Pulizia artefatti runtime
    rm -rf "$APP_PATH/Contents/MacOS/downloads"
    rm -rf "$APP_PATH/Contents/MacOS/logs"

    BINARY="$APP_PATH/Contents/MacOS/bbsclient-gui"
    echo "✓ App: $(du -sh "$APP_PATH" | cut -f1)"
    echo "  Arch: $(file "$BINARY" | sed 's/.*: //')"

    # DMG
    echo "→ Creazione DMG..."
    DMG_NAME="BBS-Client-v${VERSION}-macOS.dmg"
    DMG_PATH="$DIST_DIR/$DMG_NAME"
    DMG_TEMP="$DIST_DIR/dmg-staging"
    rm -rf "$DMG_TEMP"
    mkdir -p "$DMG_TEMP"
    cp -R "$APP_PATH" "$DMG_TEMP/"
    ln -s /Applications "$DMG_TEMP/Applications"
    hdiutil create -volname "${APP_NAME} v${VERSION}" \
        -srcfolder "$DMG_TEMP" \
        -ov -format UDZO \
        "$DMG_PATH"
    rm -rf "$DMG_TEMP"
    echo "✓ DMG: $DMG_NAME ($(du -sh "$DMG_PATH" | cut -f1))"

    # ZIP
    echo "→ Creazione ZIP..."
    ZIP_NAME="BBS-Client-v${VERSION}-macOS.zip"
    ZIP_PATH="$DIST_DIR/$ZIP_NAME"
    cd "$PROJECT_DIR/build/bin"
    zip -r -y "$ZIP_PATH" "${APP_NAME}.app"
    cd "$PROJECT_DIR"
    echo "✓ ZIP: $ZIP_NAME ($(du -sh "$ZIP_PATH" | cut -f1))"
}

# ─── Windows (cross-compile da macOS) ───
build_windows() {
    echo ""
    echo "── Windows Build ────────────────────────"

    # Verifica cross-compiler
    if ! command -v x86_64-w64-mingw32-gcc &>/dev/null; then
        echo "✗ Cross-compiler non trovato!"
        echo "  Installa con: brew install mingw-w64"
        echo "  Poi rilancia lo script."
        return 1
    fi

    wails build -platform windows/amd64

    EXE_PATH="$PROJECT_DIR/build/bin/bbsclient-gui.exe"
    if [ ! -f "$EXE_PATH" ]; then
        echo "✗ Errore: ${EXE_PATH} non trovato!"
        return 1
    fi

    # Pulizia artefatti runtime
    rm -rf "$PROJECT_DIR/build/bin/downloads"
    rm -rf "$PROJECT_DIR/build/bin/logs"

    echo "✓ EXE: $(du -sh "$EXE_PATH" | cut -f1)"

    # ZIP per Windows
    echo "→ Creazione ZIP..."
    ZIP_NAME="BBS-Client-v${VERSION}-Windows-x64.zip"
    ZIP_PATH="$DIST_DIR/$ZIP_NAME"
    cd "$PROJECT_DIR/build/bin"
    zip -r "$ZIP_PATH" bbsclient-gui.exe
    cd "$PROJECT_DIR"
    echo "✓ ZIP: $ZIP_NAME ($(du -sh "$ZIP_PATH" | cut -f1))"
}

# ─── Linux (build nativa, no cross-compile) ───
build_linux() {
    echo ""
    echo "── Linux Build ──────────────────────────"

    # Verifica che siamo su Linux
    if [ "$(uname -s)" != "Linux" ]; then
        echo "✗ La build Linux richiede una macchina Linux nativa."
        echo "  Non è possibile cross-compilare da $(uname -s)."
        echo ""
        echo "  Su una macchina Linux (Debian/Ubuntu):"
        echo "    sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev"
        echo "    go install github.com/wailsapp/wails/v2/cmd/wails@latest"
        echo "    ./scripts/build-release.sh ${VERSION} linux"
        return 1
    fi

    # Verifica dipendenze GTK/WebKit
    MISSING=""
    if ! pkg-config --exists gtk+-3.0 2>/dev/null; then
        MISSING="$MISSING libgtk-3-dev"
    fi
    if ! pkg-config --exists webkit2gtk-4.0 2>/dev/null; then
        MISSING="$MISSING libwebkit2gtk-4.0-dev"
    fi
    if [ -n "$MISSING" ]; then
        echo "✗ Dipendenze mancanti:${MISSING}"
        echo "  Installa con: sudo apt install${MISSING}"
        return 1
    fi

    wails build -platform linux/amd64

    BIN_PATH="$PROJECT_DIR/build/bin/bbsclient-gui"
    if [ ! -f "$BIN_PATH" ]; then
        echo "✗ Errore: ${BIN_PATH} non trovato!"
        return 1
    fi

    # Pulizia artefatti runtime
    rm -rf "$PROJECT_DIR/build/bin/downloads"
    rm -rf "$PROJECT_DIR/build/bin/logs"

    echo "✓ BIN: $(du -sh "$BIN_PATH" | cut -f1)"
    echo "  Arch: $(file "$BIN_PATH" | sed 's/.*: //')"

    # TAR.GZ per Linux
    echo "→ Creazione tar.gz..."
    TAR_NAME="BBS-Client-v${VERSION}-Linux-x64.tar.gz"
    TAR_PATH="$DIST_DIR/$TAR_NAME"
    cd "$PROJECT_DIR/build/bin"
    tar czf "$TAR_PATH" bbsclient-gui
    cd "$PROJECT_DIR"
    echo "✓ TAR: $TAR_NAME ($(du -sh "$TAR_PATH" | cut -f1))"
}

# ─── Esegui build ───
case "$PLATFORM" in
    macos)
        build_macos
        ;;
    windows|win)
        build_windows
        ;;
    linux)
        build_linux
        ;;
    all)
        build_macos
        rm -rf "$PROJECT_DIR/build/bin"
        build_windows
        rm -rf "$PROJECT_DIR/build/bin"
        build_linux
        ;;
    *)
        echo "✗ Piattaforma non riconosciuta: $PLATFORM"
        echo "  Usa: macos, windows, linux, all"
        exit 1
        ;;
esac

echo ""
echo "══════════════════════════════════════════"
echo "  ✓ Release v${VERSION} completata!"
echo "══════════════════════════════════════════"
echo ""
echo "  File in: $DIST_DIR/"
ls -lh "$DIST_DIR/"*.{dmg,zip,tar.gz} 2>/dev/null | awk '{print "  " $NF " (" $5 ")"}'
echo ""
