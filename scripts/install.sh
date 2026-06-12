#!/bin/sh
#
# rigsmith installer
# ------------------
# POSIX sh installer in the style of the bun/deno one-liners. Detects OS + arch,
# downloads the latest GoReleaser-built release tarball for the requested
# binary from GitHub Releases, extracts it, and installs into:
#
#     ${RIGSMITH_INSTALL:-$HOME/.local}/bin
#
# This is the script that sits behind:
#
#     curl -fsSL https://rigsmith.sh | sh           # installs both rig + relrig
#     curl -fsSL https://rigsmith.sh | sh -s rig    # just rig
#     curl -fsSL https://relrig.sh   | sh -s relrig # just relrig
#
# Usage:
#     install.sh [rig|relrig]        (default: install both)
#
# Env:
#     RIGSMITH_INSTALL   install prefix (default: $HOME/.local) -> bin/ underneath
#     RIGSMITH_VERSION   pin a release tag (default: latest)
#
set -e

# TODO: replace with the real public repo slug once it exists.
REPO="rigsmith/rigsmith"

INSTALL_PREFIX="${RIGSMITH_INSTALL:-$HOME/.local}"
BIN_DIR="$INSTALL_PREFIX/bin"
VERSION="${RIGSMITH_VERSION:-latest}"

info()  { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
warn()  { printf '\033[1;33mwarning:\033[0m %s\n' "$1" >&2; }
error() { printf '\033[1;31merror:\033[0m %s\n' "$1" >&2; exit 1; }

# --- detect OS ---------------------------------------------------------------
detect_os() {
  os="$(uname -s)"
  case "$os" in
    Linux)  echo "linux" ;;
    Darwin) echo "darwin" ;;
    *)      error "unsupported OS: $os (use Homebrew, Scoop, or download manually)" ;;
  esac
}

# --- detect arch -------------------------------------------------------------
detect_arch() {
  arch="$(uname -m)"
  case "$arch" in
    x86_64 | amd64)  echo "amd64" ;;
    arm64 | aarch64) echo "arm64" ;;
    *)               error "unsupported architecture: $arch" ;;
  esac
}

# --- resolve the release tag we should fetch ---------------------------------
resolve_version() {
  if [ "$VERSION" != "latest" ]; then
    echo "$VERSION"
    return
  fi
  # Follow GitHub's "latest" redirect to discover the newest tag.
  redirect_url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
    "https://github.com/$REPO/releases/latest")"
  tag="${redirect_url##*/tag/}"
  if [ -z "$tag" ] || [ "$tag" = "$redirect_url" ]; then
    error "could not determine the latest release tag for $REPO"
  fi
  echo "$tag"
}

# --- install a single binary -------------------------------------------------
# $1 = binary name (rig|relrig), $2 = tag, $3 = os, $4 = arch
install_binary() {
  bin="$1"
  tag="$2"
  os="$3"
  arch="$4"

  # Archive names match the GoReleaser name_template:
  #   <bin>_<version>_<os>_<arch>.tar.gz   (version is the tag without leading v)
  version_no_v="${tag#v}"
  archive="${bin}_${version_no_v}_${os}_${arch}.tar.gz"
  url="https://github.com/$REPO/releases/download/$tag/$archive"

  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  info "downloading $bin $tag ($os/$arch)"
  if ! curl -fsSL "$url" -o "$tmp/$archive"; then
    error "download failed: $url"
  fi

  info "extracting $archive"
  tar -xzf "$tmp/$archive" -C "$tmp"

  if [ ! -f "$tmp/$bin" ]; then
    error "expected '$bin' inside $archive but it was not found"
  fi

  mkdir -p "$BIN_DIR"
  install -m 0755 "$tmp/$bin" "$BIN_DIR/$bin" 2>/dev/null \
    || { cp "$tmp/$bin" "$BIN_DIR/$bin" && chmod 0755 "$BIN_DIR/$bin"; }

  rm -rf "$tmp"
  trap - EXIT

  info "installed $bin -> $BIN_DIR/$bin"
}

# --- PATH hint ---------------------------------------------------------------
path_hint() {
  case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *)
      warn "$BIN_DIR is not on your PATH."
      echo "  Add this to your shell profile (~/.bashrc, ~/.zshrc, etc.):"
      echo ""
      echo "      export PATH=\"$BIN_DIR:\$PATH\""
      echo ""
      ;;
  esac
}

main() {
  command -v curl >/dev/null 2>&1 || error "curl is required"
  command -v tar  >/dev/null 2>&1 || error "tar is required"

  target="${1:-both}"
  case "$target" in
    rig | relrig | clauderig | both) ;;
    *) error "unknown binary '$target' (expected: rig, relrig, clauderig, or omit for both)" ;;
  esac

  os="$(detect_os)"
  arch="$(detect_arch)"
  tag="$(resolve_version)"

  case "$target" in
    rig)       install_binary rig       "$tag" "$os" "$arch" ;;
    relrig)    install_binary relrig    "$tag" "$os" "$arch" ;;
    clauderig) install_binary clauderig "$tag" "$os" "$arch" ;;
    both)
      install_binary rig    "$tag" "$os" "$arch"
      install_binary relrig "$tag" "$os" "$arch"
      ;;
  esac

  path_hint
  info "done."
}

main "$@"
