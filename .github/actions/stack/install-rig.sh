#!/usr/bin/env bash
# Standalone installer: release asset first, source fallback at the same pin.
set -euo pipefail
version="${INPUT_RIG_VERSION:?Set rig-version to an exact release tag}"
[[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'rig-version must be an exact vX.Y.Z tag' >&2; exit 1; }
case "$(uname -s)" in
  Linux*) os=linux;; Darwin*) os=darwin;; MINGW*|MSYS*|CYGWIN*) os=windows;;
  *) echo 'Unsupported runner OS' >&2; exit 1;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;;
  *) echo 'Unsupported runner architecture' >&2; exit 1;;
esac
bindir="$(mktemp -d "${RUNNER_TEMP:-/tmp}/rig-bin.XXXXXX")"
ext=tar.gz; [[ "$os" == windows ]] && ext=zip
asset="rig_${version#v}_${os}_${arch}.${ext}"
if curl -fsSL -o "$bindir/archive.$ext" "https://github.com/rigsmith/rigsmith/releases/download/$version/$asset"; then
  if [[ "$ext" == zip ]]; then unzip -oq "$bindir/archive.$ext" -d "$bindir"; else tar -xzf "$bindir/archive.$ext" -C "$bindir"; fi
else
  GOBIN="$bindir" go install "github.com/rigsmith/rigsmith/cmd/rig@$version"
fi
if [[ "$os" == windows ]]; then chmod +x "$bindir/rig.exe"; else chmod +x "$bindir/rig"; fi
echo "$bindir" >> "$GITHUB_PATH"
