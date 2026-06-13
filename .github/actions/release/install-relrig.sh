#!/usr/bin/env bash
#
# Puts `relrig` on PATH for the release action. Tries to download the goreleaser
# release asset first (no toolchain, correct binary name); falls back to
# `go install` from the module proxy when no matching asset is published yet
# (e.g. during the pre-release period, or for `@main`/a branch ref).
#
# Inputs (env):
#   INPUT_RELRIG_VERSION  tag to install ("latest", "v1.2.3", "main", …). Default: latest.
#   RELRIG_REPO           owner/name to download release assets from. Default: rigsmith/rigsmith.
#   GITHUB_TOKEN          used (if present) to lift the GitHub API rate limit.
set -euo pipefail

version="${INPUT_RELRIG_VERSION:-latest}"
repo="${RELRIG_REPO:-rigsmith/rigsmith}"

# Map the runner to goreleaser's {os}_{arch} asset suffix.
case "$(uname -s)" in
  Linux*)  os="linux" ;;
  Darwin*) os="darwin" ;;
  MINGW* | MSYS* | CYGWIN*) os="windows" ;;
  *) os="$(uname -s | tr '[:upper:]' '[:lower:]')" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *) arch="$(uname -m)" ;;
esac
ext="tar.gz"; [[ "${os}" == "windows" ]] && ext="zip"

bindir="${RUNNER_TEMP:-/tmp}/relrig-bin"
mkdir -p "${bindir}"

# Built as a string (not an array) so empty expansion is safe under `set -u` on
# macOS's bash 3.2 runners.
auth=""
[[ -n "${GITHUB_TOKEN:-}" ]] && auth="-H \"Authorization: Bearer ${GITHUB_TOKEN}\""

curl_auth() { eval "curl ${auth} \"\$@\""; }

# Resolve "latest" to a concrete tag via the releases API.
tag="${version}"
if [[ "${tag}" == "latest" ]]; then
  tag="$(curl_auth -fsSL "https://api.github.com/repos/${repo}/releases/latest" 2>/dev/null \
    | grep -oE '"tag_name"[[:space:]]*:[[:space:]]*"[^"]+"' | head -1 | sed -E 's/.*"([^"]+)"$/\1/' || true)"
fi

download_asset() {
  [[ -z "${tag}" ]] && return 1
  local ver="${tag#v}"
  local asset="relrig_${ver}_${os}_${arch}.${ext}"
  local url="https://github.com/${repo}/releases/download/${tag}/${asset}"
  echo "Downloading ${url}"
  local tmp="${bindir}/${asset}"
  curl_auth -fsSL -o "${tmp}" "${url}" 2>/dev/null || return 1
  if [[ "${ext}" == "zip" ]]; then unzip -oq "${tmp}" -d "${bindir}"; else tar -xzf "${tmp}" -C "${bindir}"; fi
  rm -f "${tmp}"
  [[ -f "${bindir}/relrig" || -f "${bindir}/relrig.exe" ]]
}

install_from_source() {
  command -v go >/dev/null 2>&1 || { echo "go toolchain not found; cannot fall back to 'go install'." >&2; return 1; }
  local ref="${version}"; [[ "${ref}" == "latest" ]] && ref="${tag:-latest}"
  echo "Building relrig from source: go install github.com/rigsmith/release@${ref}"
  GOBIN="${bindir}" go install "github.com/rigsmith/release@${ref}"
  # The module's main package compiles to `release`; expose it as `relrig`.
  if [[ -f "${bindir}/release" && ! -f "${bindir}/relrig" ]]; then mv "${bindir}/release" "${bindir}/relrig"; fi
  [[ -f "${bindir}/relrig" ]]
}

if download_asset; then
  echo "Installed relrig from release asset (${tag})."
elif install_from_source; then
  echo "Installed relrig from source."
else
  echo "::error::Could not install relrig (no release asset for '${version}' and 'go install' fallback failed)." >&2
  exit 1
fi

chmod +x "${bindir}/relrig" 2>/dev/null || true
echo "${bindir}" >> "${GITHUB_PATH}"
"${bindir}/relrig" --version 2>/dev/null || true
