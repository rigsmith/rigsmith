#!/usr/bin/env bash
# Release-only installer. Download or verification failures are fatal.
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
curl -fsSL -o "$bindir/archive.$ext" "https://github.com/rigsmith/rigsmith/releases/download/$version/$asset"
curl -fsSL -o "$bindir/checksums.txt" "https://github.com/rigsmith/rigsmith/releases/download/$version/checksums.txt"
want="$(awk -v asset="$asset" '$2 == asset { print $1 }' "$bindir/checksums.txt")"
[[ "$want" =~ ^[0-9a-fA-F]{64}$ ]] || { echo 'Missing or invalid release checksum' >&2; exit 1; }
if command -v sha256sum >/dev/null; then got="$(sha256sum "$bindir/archive.$ext")"; else got="$(shasum -a 256 "$bindir/archive.$ext")"; fi
got="${got%% *}"
[[ "$got" == "$want" ]] || { echo 'rig checksum mismatch' >&2; exit 1; }
if [[ -n "${INPUT_RIG_SHA256:-}" ]]; then
  [[ "$got" == "$INPUT_RIG_SHA256" ]] || { echo 'rig pinned checksum mismatch' >&2; exit 1; }
fi
if [[ "$ext" == zip ]]; then unzip -oq "$bindir/archive.$ext" -d "$bindir"; else tar -xzf "$bindir/archive.$ext" -C "$bindir"; fi
if [[ "$os" == windows ]]; then chmod +x "$bindir/rig.exe"; else chmod +x "$bindir/rig"; fi
echo "$bindir" >> "$GITHUB_PATH"
