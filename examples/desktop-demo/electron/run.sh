#!/usr/bin/env bash
# Drive the full shiprig release cycle for the empty-window Electron app:
#
#   shiprig status        — show the pending changeset + planned bump, and that
#                           the app is owned by the `electron` ecosystem
#   shiprig version       — apply the bump (stamps package.json "version")
#   shiprig release --dry-build
#                         — run only the build step (no registry/forge/git side
#                           effects): electron-builder produces the installer
#
# It works on a throwaway copy in a tempdir, so this directory stays clean
# (no node_modules/, no dist/). Uses the shiprig on your PATH, or set
# SHIPRIG=/path/to/shiprig; with neither it builds one from this repo.
#
# Requirements: Node.js + npm. The build step downloads Electron and
# electron-builder on first run (~hundreds of MB) and produces a platform
# installer (.dmg on macOS, .AppImage on Linux, NSIS .exe on Windows).
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
# Copy the app sources (not node_modules/dist) into the throwaway workspace.
cp -R "$here/." "$work/"
rm -rf "$work/node_modules" "$work/dist"

git -C "$work" init -q
git -C "$work" config user.email demo@example.com
git -C "$work" config user.name demo
git -C "$work" add -A
git -C "$work" commit -qm init

echo
echo "############################################################"
echo "#  shiprig status   (app owned by the 'electron' ecosystem) #"
echo "############################################################"
( cd "$work" && "$SHIPRIG" status )

echo
echo "############################################################"
echo "#  shiprig version  (stamps package.json)                   #"
echo "############################################################"
( cd "$work" && "$SHIPRIG" version )
echo "→ package.json version is now:"
grep '"version"' "$work/package.json" | head -1

if ! command -v npm >/dev/null 2>&1; then
  echo
  echo "npm not found — skipping the build step. Install Node.js to build the installer."
  exit 0
fi

echo
echo "############################################################"
echo "#  shiprig release --dry-build   (electron-builder → binary) #"
echo "############################################################"
( cd "$work" && npm install --no-audit --no-fund && "$SHIPRIG" release --dry-build )

echo
echo "→ produced artifacts:"
find "$work/dist" -maxdepth 1 -type f \( -name '*.dmg' -o -name '*.AppImage' -o -name '*.exe' -o -name '*.deb' -o -name '*.yml' \) -print 2>/dev/null || true
