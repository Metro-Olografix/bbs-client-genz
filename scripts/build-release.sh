#!/bin/bash
# ─────────────────────────────────────────────────
# build-release.sh — Crea release distribuibili
# Uso: ./scripts/build-release.sh [versione] [piattaforma]
# Es:  ./scripts/build-release.sh 0.9.1
#      ./scripts/build-release.sh 0.9.1 macos
#      ./scripts/build-release.sh 0.9.1 windows
#      ./scripts/build-release.sh 0.9.1 all
# Piattaforme: macos, windows, all (default: macos)
# ─────────────────────────────────────────────────

set -e

VERSION="${1:-0.9.1}"
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

# ─── Esegui build ───
case "$PLATFORM" in
    macos)
        build_macos
        ;;
    windows|win)
        build_windows
        ;;
    all)
        build_macos
        rm -rf "$PROJECT_DIR/build/bin"
        build_windows
        ;;
    *)
        echo "✗ Piattaforma non riconosciuta: $PLATFORM"
        echo "  Usa: macos, windows, all"
        exit 1
        ;;
esac

echo ""
echo "══════════════════════════════════════════"
echo "  ✓ Release v${VERSION} completata!"
echo "══════════════════════════════════════════"
echo ""
echo "  File in: $DIST_DIR/"
ls -lh "$DIST_DIR/"*.{dmg,zip} 2>/dev/null | awk '{print "  " $NF " (" $5 ")"}'
echo ""
