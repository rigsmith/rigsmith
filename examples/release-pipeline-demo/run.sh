#!/usr/bin/env bash
# Self-contained demo of shiprig's fully-customizable release pipeline.
#
# Spins up a throwaway two-package Node workspace, drops in release.jsonc (which
# replaces every built-in step with an echo), and runs the pipeline twice:
#   1. shiprig release --dry-run   — preview + dry-run-enabled commands
#   2. shiprig release --yes       — the real run (every step is an echo, so it
#                                    is completely safe and touches nothing)
#
# Uses the shiprig on your PATH, or set SHIPRIG=/path/to/shiprig. With neither,
# it builds one from this repo.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"

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
mkdir -p "$work/.changeset" "$work/packages/web" "$work/packages/api"

printf '{ "name": "@acme/web", "version": "2.1.0" }\n' > "$work/packages/web/package.json"
printf '{ "name": "@acme/api", "version": "1.4.0" }\n' > "$work/packages/api/package.json"
printf '{ "name": "root", "private": true, "workspaces": ["packages/*"] }\n' > "$work/package.json"
printf '{ "baseBranch": "main" }\n' > "$work/.changeset/config.json"
cp "$here/release.jsonc" "$work/.changeset/release.jsonc"

git -C "$work" init -q
git -C "$work" config user.email demo@example.com
git -C "$work" config user.name demo
git -C "$work" add -A
git -C "$work" commit -qm init

echo
echo "############################################################"
echo "#  shiprig release --dry-run                                #"
echo "############################################################"
echo
( cd "$work" && "$SHIPRIG" release --dry-run )

echo
echo "############################################################"
echo "#  shiprig release --yes   (real run — every step echoes)   #"
echo "############################################################"
echo
( cd "$work" && "$SHIPRIG" release --yes )
