#!/bin/sh
# Build, sign and notarize the claudeRig UI as a macOS .app bundle.
#
# Deliberately NOT part of the GoReleaser run. Those builds cross-compile from
# Linux and quill signs the bare Mach-O binaries, which is what keeps the
# release cheap. The UI breaks all three assumptions: it needs cgo (so a real
# macOS runner), it ships as a bundle rather than a binary, and quill signs
# Mach-O files, not bundles.
#
# It reuses the CREDENTIALS though — the same Developer ID certificate and the
# same App Store Connect key the notarize block feeds to quill. Nothing here is
# a second signing identity, only a second consumer of the one that exists.
#
#   $1  version, e.g. 1.2.3 (no leading v)
#   $2  output directory
#
# Env (all optional — without them the bundle is built unsigned, which still
# runs locally and keeps this script usable before the secrets are wired):
#   MACOS_SIGN_P12 / MACOS_SIGN_PASSWORD          base64 .p12 + its password
#   MACOS_NOTARY_ISSUER_ID / _KEY_ID / _KEY       App Store Connect API key
set -eu

version="${1:?version required}"
outdir="${2:?output directory required}"
repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

app_name="claudeRigUi"
bundle_id="dev.rigsmith.clauderig-ui"   # matches ui/main.go BundleID
app="$outdir/$app_name.app"

mkdir -p "$outdir"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"

# Universal, so one download serves both architectures and the cask needs no
# arch logic. MACOSX_DEPLOYMENT_TARGET matches what the CLIs build against.
echo "  building universal binary"
for arch in arm64 amd64; do
  ( cd "$repo" && CGO_ENABLED=1 GOOS=darwin GOARCH="$arch" \
      MACOSX_DEPLOYMENT_TARGET=12.0 CGO_LDFLAGS=-mmacosx-version-min=12.0 \
      go build -trimpath -ldflags "-s -w -X main.version=$version" \
      -o "$outdir/.$app_name-$arch" ./ui )
done
lipo -create -output "$app/Contents/MacOS/$app_name" \
  "$outdir/.$app_name-arm64" "$outdir/.$app_name-amd64"
rm -f "$outdir/.$app_name-arm64" "$outdir/.$app_name-amd64"
chmod +x "$app/Contents/MacOS/$app_name"

# LSUIElement because this is a menu bar app: it has no Dock icon and no main
# window at launch, which is also what ActivationPolicyAccessory says at
# runtime. Declaring it in both keeps a Dock icon from flashing up before the
# app gets to say otherwise.
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>$app_name</string>
  <key>CFBundleDisplayName</key><string>$app_name</string>
  <key>CFBundleExecutable</key><string>$app_name</string>
  <key>CFBundleIdentifier</key><string>$bundle_id</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$version</string>
  <key>CFBundleVersion</key><string>$version</string>
  <key>CFBundleIconFile</key><string>icon</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>LSUIElement</key><true/>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

# Generated from the design mark rather than a committed .icns: design/ is the
# single source for the brand, and a binary icon checked in beside it is a copy
# that silently stops matching. Capped at 512 because that is what the mark is —
# claiming a 1024 retina slot by upscaling would look worse than not offering
# one.
icon_src="$repo/design/marks/png/app-claudeRig-512.png"
if [ -f "$icon_src" ]; then
  echo "  building the icon"
  iconset="$outdir/icon.iconset"
  rm -rf "$iconset"
  mkdir -p "$iconset"
  for size in 16 32 128 256 512; do
    sips -z "$size" "$size" "$icon_src" --out "$iconset/icon_${size}x${size}.png" >/dev/null
    half=$((size / 2))
    [ "$half" -ge 16 ] && cp "$iconset/icon_${size}x${size}.png" "$iconset/icon_${half}x${half}@2x.png"
  done
  iconutil -c icns "$iconset" -o "$app/Contents/Resources/icon.icns"
  rm -rf "$iconset"
else
  # Not fatal: an app with the generic icon is a cosmetic problem, and failing
  # the release over one would be worse than shipping it.
  echo "  no $icon_src — shipping with the generic icon"
fi

if [ -z "${MACOS_SIGN_P12:-}" ]; then
  echo "  MACOS_SIGN_P12 unset — leaving the bundle unsigned"
else
  echo "  signing"
  keychain="$outdir/build.keychain"
  pass="$(uuidgen)"
  security create-keychain -p "$pass" "$keychain"
  security set-keychain-settings -lut 3600 "$keychain"
  security unlock-keychain -p "$pass" "$keychain"
  printf '%s' "$MACOS_SIGN_P12" | base64 --decode > "$outdir/.cert.p12"
  security import "$outdir/.cert.p12" -k "$keychain" -P "${MACOS_SIGN_PASSWORD:-}" \
    -T /usr/bin/codesign
  rm -f "$outdir/.cert.p12"
  security set-key-partition-list -S apple-tool:,apple: -s -k "$pass" "$keychain" >/dev/null
  security list-keychains -d user -s "$keychain" $(security list-keychains -d user | tr -d '"')

  identity=$(security find-identity -v -p codesigning "$keychain" | awk '/Developer ID Application/ {print $2; exit}')
  [ -n "$identity" ] || { echo "no Developer ID Application identity in the keychain" >&2; exit 1; }

  # --options runtime is required for notarization; --deep because the bundle
  # is a directory and the executable inside it has to be signed too.
  codesign --force --deep --timestamp --options runtime \
    --sign "$identity" "$app"
  codesign --verify --strict --verbose=2 "$app"
fi

echo "  zipping"
zip_name="${app_name}_${version}_darwin_universal.zip"
# ditto, not zip: it preserves the bundle's symlinks and extended attributes,
# and it is what notarytool expects to be handed.
( cd "$outdir" && ditto -c -k --keepParent "$app_name.app" "$zip_name" )

if [ -n "${MACOS_NOTARY_KEY:-}" ]; then
  echo "  notarizing"
  printf '%s' "$MACOS_NOTARY_KEY" | base64 --decode > "$outdir/.notary.p8"
  xcrun notarytool submit "$outdir/$zip_name" \
    --issuer "$MACOS_NOTARY_ISSUER_ID" \
    --key-id "$MACOS_NOTARY_KEY_ID" \
    --key "$outdir/.notary.p8" \
    --wait --timeout 20m
  rm -f "$outdir/.notary.p8"
  # Staple the app, then re-zip: the ticket lives in the bundle, so a zip made
  # before stapling carries an unstapled app and Gatekeeper has to phone home.
  xcrun stapler staple "$app"
  rm -f "$outdir/$zip_name"
  ( cd "$outdir" && ditto -c -k --keepParent "$app_name.app" "$zip_name" )
else
  echo "  MACOS_NOTARY_KEY unset — not notarizing"
fi

echo "$outdir/$zip_name"
