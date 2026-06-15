#!/usr/bin/env bash
# Drive the full shiprig release cycle for the empty-window Tauri app:
#
#   shiprig status        — show the pending changeset + planned bump, and that
#                           the app is owned by the `tauri` ecosystem (not cargo)
#   shiprig version       — apply the bump. Because tauri.conf.json carries the
#                           version (conf-sourced), shiprig stamps BOTH
#                           tauri.conf.json and Cargo.toml in lockstep
#   shiprig release --dry-build
#                         — run only the build step (no registry/forge/git side
#                           effects): `cargo tauri build` produces the installer
#
# Works on a throwaway copy in a tempdir, so this directory stays clean. Uses the
# shiprig on your PATH, or set SHIPRIG=/path/to/shiprig; with neither it builds
# one from this repo.
#
# Requirements for the build step: Rust + the Tauri CLI (`cargo install
# tauri-cli --version '^2'`) and your platform's Tauri system deps
# (https://tauri.app/start/prerequisites/). The version/status steps need
# neither — they only read/stamp files, which is what shiprig's tauri adapter
# does. The build downloads crates on first run and produces a platform
# installer (.dmg/.app on macOS, .deb/.AppImage on Linux, .msi/NSIS on Windows).
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../../.." && pwd)"

SHIPRIG="${SHIPRIG:-}"
if [ -z "$SHIPRIG" ]; then
  if command -v shiprig >/dev/null 2>&1; then
    SHIPRIG="shiprig"
  else
    SHIPRIG="$(mktemp -d)/shiprig"
    echo "» building shiprig from $repo …"
    ( cd "$repo" && go build -o "$SHIPRIG" ./cmd/shiprig )
  fi
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cp -R "$here/." "$work/"
rm -rf "$work/src-tauri/target" "$work/src-tauri/icons"

git -C "$work" init -q
git -C "$work" config user.email demo@example.com
git -C "$work" config user.name demo
git -C "$work" add -A
git -C "$work" commit -qm init

echo
echo "############################################################"
echo "#  shiprig status   (app owned by the 'tauri' ecosystem)    #"
echo "############################################################"
( cd "$work" && "$SHIPRIG" status )

echo
echo "############################################################"
echo "#  shiprig version  (stamps tauri.conf.json + Cargo.toml)   #"
echo "############################################################"
( cd "$work" && "$SHIPRIG" version )
echo "→ versions after the bump (lockstep):"
echo "   tauri.conf.json: $(grep '"version"' "$work/src-tauri/tauri.conf.json" | head -1 | tr -d ' ,')"
echo "   Cargo.toml:      $(grep '^version' "$work/src-tauri/Cargo.toml" | head -1)"

if ! { command -v cargo >/dev/null 2>&1 && cargo tauri --version >/dev/null 2>&1; }; then
  echo
  echo "Rust + the Tauri CLI not found — skipping the build step."
  echo "Install them (cargo install tauri-cli --version '^2') to build the installer."
  exit 0
fi

# Tauri needs app icons to bundle. Generate a set from a tiny placeholder PNG so
# the demo is self-contained; replace app-icon.png with your own 1024x1024 art
# (then re-run `cargo tauri icon`) for a real icon.
echo "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==" | base64 --decode > "$work/app-icon.png"
( cd "$work/src-tauri" && cargo tauri icon "$work/app-icon.png" )

echo
echo "############################################################"
echo "#  shiprig release --dry-build   (cargo tauri build → binary)#"
echo "############################################################"
( cd "$work" && "$SHIPRIG" release --dry-build )

echo
echo "→ produced bundles:"
find "$work/src-tauri/target" -path '*/release/bundle/*' -type f 2>/dev/null | head -20 || true
