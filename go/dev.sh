#!/usr/bin/env bash
# TNT dev runner.
# - Stands up a plain static HTTP server on :34115 serving ./frontend.
#   Open http://localhost:34115 in any browser. CSS/HTML/JS edits auto-reload
#   via a token file the page polls. Go bindings (window.go.main.*) won't work
#   in browser mode — for that, use the native app side below.
# - Also builds and launches the native TNT.app pointed at ./frontend on disk
#   (so the same source files drive both the browser and the desktop window).
#
# Usage: ./dev.sh           # full setup (server + native window)
#        ./dev.sh --browser # browser only, no native window build/launch
#        Ctrl+C to stop.

set -u
cd "$(dirname "$0")"

PORT=34115
APP=./build/bin/TNT.app
BIN="$APP/Contents/MacOS/TNT"
FRONTEND="$(pwd)/frontend"
RELOAD_FILE="$FRONTEND/reload-token.txt"

BROWSER_ONLY=0
[ "${1-}" = "--browser" ] && BROWSER_ONLY=1

# Initial token so the page captures a baseline.
date +%s%N > "$RELOAD_FILE"

# Free the port if something held it.
PID_ON_PORT=$(lsof -ti :$PORT 2>/dev/null || true)
[ -n "$PID_ON_PORT" ] && kill $PID_ON_PORT 2>/dev/null && sleep 0.3

echo "[dev.sh] static HTTP on http://localhost:$PORT (serving $FRONTEND)"
( cd "$FRONTEND" && python3 -m http.server $PORT --bind 127.0.0.1 ) >/tmp/tnt-http.log 2>&1 &
HTTP_PID=$!
sleep 0.4
if ! kill -0 $HTTP_PID 2>/dev/null; then
    echo "[dev.sh] HTTP server failed to start:"
    cat /tmp/tnt-http.log
    exit 1
fi

TNT_PID=""
if [ $BROWSER_ONLY -eq 0 ]; then
    echo "[dev.sh] building (codesign error ignored)"
    wails build -platform darwin/arm64 -devtools >/tmp/tnt-build.log 2>&1 || true

    if [ ! -x "$BIN" ]; then
        echo "[dev.sh] build failed — $BIN not found:"
        cat /tmp/tnt-build.log
        kill $HTTP_PID 2>/dev/null
        exit 1
    fi

    pkill -f "$BIN" 2>/dev/null || true
    sleep 0.3

    echo "[dev.sh] launching TNT with assetdir=$FRONTEND"
    assetdir="$FRONTEND" "$BIN" >/tmp/tnt.log 2>&1 &
    TNT_PID=$!
    sleep 1
    osascript -e 'tell application "TNT" to activate' >/dev/null 2>&1 || true
fi

cleanup() {
    echo "[dev.sh] stopping"
    [ -n "$TNT_PID" ] && kill $TNT_PID 2>/dev/null
    kill $HTTP_PID 2>/dev/null
    rm -f "$RELOAD_FILE"
    exit 0
}
trap cleanup INT TERM

echo "[dev.sh] watching $FRONTEND — saved CSS/HTML/JS triggers reload"
echo "[dev.sh] open http://localhost:$PORT in your browser"

last_hash=""
while true; do
    if ! kill -0 $HTTP_PID 2>/dev/null; then
        echo "[dev.sh] HTTP server died"
        break
    fi
    if [ -n "$TNT_PID" ] && ! kill -0 $TNT_PID 2>/dev/null; then
        echo "[dev.sh] TNT exited"
        break
    fi
    new_hash=$(find "$FRONTEND" -type f \( -name "*.html" -o -name "*.css" -o -name "*.js" \) ! -name "reload-token.txt" -exec stat -f "%m %N" {} \; 2>/dev/null | sort | md5)
    if [ -n "$last_hash" ] && [ "$new_hash" != "$last_hash" ]; then
        echo "[dev.sh] change → reload"
        date +%s%N > "$RELOAD_FILE"
    fi
    last_hash="$new_hash"
    sleep 0.4
done

cleanup
