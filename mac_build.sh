#!/bin/bash
set -e

# Build location
BUILD_DIR="/tmp/tnt-build-$(date +%s)"
SOURCE_DIR="$(pwd)"

# Clean and create build directory
rm -rf /tmp/tnt-build-*
mkdir -p "$BUILD_DIR"

# Copy source
cp -r "$SOURCE_DIR"/* "$BUILD_DIR/"
cd "$BUILD_DIR"

# Remove any existing builds
rm -f tnt *.dmg
rm -rf TNT.app

# Clean build with proper flags
go clean
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -buildmode=pie -ldflags="-s -w" -o tnt

# Package with Fyne
fyne package -os darwin -name TNT -icon Icon.png -executable tnt

# Aggressively remove extended attributes
find TNT.app -print0 | xargs -0 xattr -c
xattr -cr TNT.app

# Sign app
codesign --force --deep --options runtime --sign "Developer ID Application: Collins Group oy (478AR6Y9JJ)" TNT.app

# Verify
codesign --verify --deep --strict --verbose=2 TNT.app

# Create and sign DMG
npx create-dmg TNT.app --overwrite --dmg-title="TNT"
mv "TNT"*.dmg "TNT.dmg"
DMG_FILE="TNT.dmg"

# Find the created DMG (whatever version it is)
DMG_FILE=$(ls TNT*.dmg)

# Submit for notarization
xcrun notarytool submit "$DMG_FILE" --keychain-profile "notary-profile" --wait

# Staple
xcrun stapler staple "$DMG_FILE"

# Copy back to source
cp "$DMG_FILE" "$SOURCE_DIR/"

echo "Done! DMG is at $SOURCE_DIR/$DMG_FILE"