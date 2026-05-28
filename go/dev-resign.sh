#!/usr/bin/env bash
# Workaround for macOS provenance-attribute codesign bug that breaks `wails dev`.
# Strips the bundle via ditto (fresh inode, no xattrs), ad-hoc signs, and relaunches.
set -euo pipefail

APP="$(cd "$(dirname "$0")" && pwd)/build/bin/TNT.app"

if [ ! -d "$APP" ]; then
    echo "No bundle at $APP — run 'wails dev' or 'wails build' first."
    exit 1
fi

TMP="${APP}.new"
rm -rf "$TMP"
ditto --norsrc --noextattr --noacl "$APP" "$TMP"
rm -rf "$APP"
mv "$TMP" "$APP"
codesign -f -s - --deep "$APP" >/dev/null
echo "Signed: $APP"

if [ "${1-}" = "--launch" ]; then
    pkill -f "$APP/Contents/MacOS/TNT" 2>/dev/null || true
    sleep 0.2
    open "$APP"
fi
