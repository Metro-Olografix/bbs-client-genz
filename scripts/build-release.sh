#!/bin/bash
# ─────────────────────────────────────────────────
# build-release.sh — Crea una release macOS distribuibile
# Uso: ./scripts/build-release.sh [versione]
# Es:  ./scripts/build-release.sh 0.9.0
# ─────────────────────────────────────────────────

set -e

VERSION="${1:-0.9.0}"
APP_NAME="BBS Client for Gen-Z"
PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
DIST_DIR="$PROJECT_DIR/dist"
APP_PATH="$PROJECT_DIR/build/bin/${APP_NAME}.app"

echo "══════════════════════════════════════════"
echo "  Building ${APP_NAME} v${VERSION}"
echo "══════════════════════════════════════════"

# 1. Pulisci build precedenti
echo "→ Pulizia build precedenti..."
rm -rf "$PROJECT_DIR/build/bin"
rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

# 2. Build con Wails
echo "→ Building con Wails..."
cd "$PROJECT_DIR"
wails build

# 3. Verifica che il .app sia stato creato
if [ ! -d "$APP_PATH" ]; then
    echo "✗ Errore: ${APP_PATH} non trovato!"
    exit 1
fi
echo "✓ App generata: $(du -sh "$APP_PATH" | cut -f1)"

# 4. Pulizia: rimuovi downloads/logs generati dentro il .app
echo "→ Pulizia artefatti runtime..."
rm -rf "$APP_PATH/Contents/MacOS/downloads"
rm -rf "$APP_PATH/Contents/MacOS/logs"

# 5. Info sul binario
BINARY="$APP_PATH/Contents/MacOS/bbsclient-gui"
echo "→ Binario: $(du -sh "$BINARY" | cut -f1)"
echo "→ Architettura: $(file "$BINARY" | sed 's/.*: //')"

# 6. Crea DMG
echo "→ Creazione DMG..."
DMG_NAME="BBS-Client-v${VERSION}-macOS.dmg"
DMG_PATH="$DIST_DIR/$DMG_NAME"

# Crea cartella temporanea per il DMG
DMG_TEMP="$DIST_DIR/dmg-staging"
mkdir -p "$DMG_TEMP"
cp -R "$APP_PATH" "$DMG_TEMP/"

# Aggiungi link a /Applications per drag & drop
ln -s /Applications "$DMG_TEMP/Applications"

# Crea il DMG
hdiutil create -volname "${APP_NAME} v${VERSION}" \
    -srcfolder "$DMG_TEMP" \
    -ov -format UDZO \
    "$DMG_PATH"

# Pulizia staging
rm -rf "$DMG_TEMP"

echo ""
echo "══════════════════════════════════════════"
echo "  ✓ Release v${VERSION} pronta!"
echo "══════════════════════════════════════════"
echo ""
echo "  DMG: $DMG_PATH"
echo "  Size: $(du -sh "$DMG_PATH" | cut -f1)"
echo ""
echo "  Per distribuire: invia il file .dmg"
echo "  L'utente lo apre, trascina l'app in Applications"
echo ""

# 7. Crea anche un .zip come alternativa
echo "→ Creazione ZIP..."
ZIP_NAME="BBS-Client-v${VERSION}-macOS.zip"
ZIP_PATH="$DIST_DIR/$ZIP_NAME"
cd "$PROJECT_DIR/build/bin"
zip -r -y "$ZIP_PATH" "${APP_NAME}.app"

echo "  ZIP: $ZIP_PATH"
echo "  Size: $(du -sh "$ZIP_PATH" | cut -f1)"
echo ""
echo "══════════════════════════════════════════"
echo "  Distribuzione completata!"
echo "══════════════════════════════════════════"
