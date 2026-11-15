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
#     curl -fsSL https://rigsmith.sh | sh            # installs all tools
#     curl -fsSL https://rigsmith.sh | sh -s rig     # just rig
#     curl -fsSL https://rigsmith.sh | sh -s shiprig # just shiprig
#
# Usage:
#     install.sh [rig|shiprig|clauderig|codexrig|changerig|brewrig|all]   (default: all)
#
# Env:
#     RIGSMITH_INSTALL   install prefix (default: $HOME/.local) -> bin/ underneath
#     RIGSMITH_BIN_DIR   install into exactly this directory (overrides RIGSMITH_INSTALL)
#     RIGSMITH_VERSION   pin a release tag (default: latest)
#     RIGSMITH_REPO      GitHub repo to download from (default: rigsmith/rigsmith)
#     RIGSMITH_REQUIRE_SIGNATURE=1
#                        refuse to install when cosign isn't there to check the
#                        signature (default: warn, and check the checksums only)
#
# `<tool> self-update` runs this with all four set: the release it found, the
# directory the running tool lives in, and the repo it checked.
#
# Verification: every release publishes checksums.txt (the SHA-256 of
# install.sh and of every archive) and checksums.txt.sigstore.json, a Sigstore
# bundle signing it with the release workflow's GitHub OIDC identity. Every
# archive is downloaded and checked against checksums.txt before any is
# installed; a mismatch, or a missing or invalid signature, installs nothing.
# Checking the signature needs cosign; without it the checksums are still
# checked, and the script says so.
#
set -e

REPO="${RIGSMITH_REPO:-rigsmith/rigsmith}"

INSTALL_PREFIX="${RIGSMITH_INSTALL:-$HOME/.local}"
BIN_DIR="${RIGSMITH_BIN_DIR:-$INSTALL_PREFIX/bin}"
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

# --- verification ------------------------------------------------------------
# A SHA-256 tool, found once up front: sha256sum (Linux), shasum (macOS), or
# openssl. Named here rather than inside $(...), where error() can't stop us.
pick_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then SHA256="sha256sum"
  elif command -v shasum >/dev/null 2>&1; then SHA256="shasum -a 256"
  elif command -v openssl >/dev/null 2>&1; then SHA256="openssl dgst -sha256 -r"
  else error "need sha256sum, shasum or openssl to verify the downloads"
  fi
}

sha256_of() { $SHA256 "$1" | awk '{print $1}'; }

# $1 = checksums file, $2 = file name: the checksum listed for it ("" if none).
listed_sum() { awk -v n="$2" '$2 == n || $2 == "*" n { print $1; exit }' "$1"; }

# $1 = tag, $2 = work dir: download checksums.txt and its signature, and check
# the signature (with cosign) before anything is trusted.
fetch_checksums() {
  base="https://github.com/$REPO/releases/download/$1"
  curl -fsSL "$base/checksums.txt" -o "$2/checksums.txt" \
    || error "could not download checksums.txt for $1 — refusing to install unverified files"
  curl -fsSL "$base/checksums.txt.sigstore.json" -o "$2/checksums.txt.sigstore.json" \
    || error "$1 has no signature for its checksums.txt (checksums.txt.sigstore.json is missing) — refusing to install"

  # A cosign that can't be trusted (cosign_usable) counts as no cosign.
  why="cosign is not installed"
  usable=0
  if command -v cosign >/dev/null 2>&1; then
    if cosign_usable; then usable=1; else why="$COSIGN_PROBLEM"; fi
  fi

  if [ "$usable" = 1 ]; then
    repo_re="$(printf '%s' "$REPO" | sed 's/[.]/\\./g')"
    # $COSIGN_EXTRA is empty or one flag; unquoted on purpose.
    # shellcheck disable=SC2086
    if ! cosign verify-blob $COSIGN_EXTRA \
        --bundle "$2/checksums.txt.sigstore.json" \
        --certificate-identity-regexp "^https://github.com/$repo_re/\\.github/workflows/release\\.yml@" \
        --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
        "$2/checksums.txt" >"$2/cosign.log" 2>&1; then
      cat "$2/cosign.log" >&2
      error "the signature on checksums.txt for $1 does not verify — refusing to install"
    fi
    info "verified the signature on checksums.txt"
  elif [ "${RIGSMITH_REQUIRE_SIGNATURE:-}" = "1" ]; then
    error "$why, so the signature on checksums.txt can't be checked, and RIGSMITH_REQUIRE_SIGNATURE=1 requires it"
  else
    warn "$why: checking every download against checksums.txt, but not checksums.txt's own signature."
    echo "  Install cosign v3.1.3 or later (https://docs.sigstore.dev/cosign/system_config/installation/) to verify it," >&2
    echo "  or set RIGSMITH_REQUIRE_SIGNATURE=1 to refuse to install without it." >&2
  fi
}

# Whether the cosign on PATH can be trusted to check the signature. Sets
# COSIGN_EXTRA (verify-blob flags for that version) or COSIGN_PROBLEM.
#   - v2 up to 2.6.4 and v3 up to 3.1.2 pass a legacy bundle carrying a bare
#     public key without checking the identity (GHSA-fx35-mq7g-6g98).
#   - Releases are signed in the new bundle format: v3 reads it by default, v2
#     only with --new-bundle-format.
cosign_usable() {
  COSIGN_EXTRA=""
  v="$(cosign version 2>&1 | sed -n 's/^GitVersion:[[:space:]]*v\{0,1\}\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' | head -n1)"
  if [ -z "$v" ]; then COSIGN_PROBLEM="couldn't read cosign's version"; return 1; fi
  maj="${v%%.*}"; rest="${v#*.}"; min="${rest%%.*}"; pat="${rest#*.}"
  at() { [ "$min" -gt "$1" ] || { [ "$min" -eq "$1" ] && [ "$pat" -ge "$2" ]; }; }
  bypass="cosign v$v has a signature-check bypass (GHSA-fx35-mq7g-6g98)"
  if [ "$maj" -ge 4 ]; then return 0; fi
  if [ "$maj" -eq 3 ]; then
    at 1 3 && return 0
    COSIGN_PROBLEM="$bypass; upgrade to v3.1.3 or later"; return 1
  fi
  if [ "$maj" -eq 2 ]; then
    if at 6 5; then COSIGN_EXTRA="--new-bundle-format"; return 0; fi
    COSIGN_PROBLEM="$bypass; upgrade to v2.6.5 or later"; return 1
  fi
  COSIGN_PROBLEM="cosign v$v is too old to read the release's signature; upgrade to v3.1.3 or later"; return 1
}

# --- download and verify a single binary's archive ---------------------------
# $1 = binary name, $2 = tag, $3 = os, $4 = arch, $5 = work dir
download_binary() {
  # Archive names match the GoReleaser name_template:
  #   <bin>_<version>_<os>_<arch>.tar.gz   (version is the tag without leading v)
  archive="${1}_${2#v}_${3}_${4}.tar.gz"
  url="https://github.com/$REPO/releases/download/$2/$archive"

  info "downloading $1 $2 ($3/$4)"
  curl -fsSL "$url" -o "$5/$archive" || error "download failed: $url"

  want="$(listed_sum "$5/checksums.txt" "$archive")"
  [ -n "$want" ] || error "$archive is not listed in checksums.txt for $2 — refusing to install"
  got="$(sha256_of "$5/$archive")"
  [ "$got" = "$want" ] \
    || error "$archive does not match checksums.txt (expected $want, got $got) — refusing to install anything"
}

# --- install a single, already verified binary --------------------------------
# $1 = binary name, $2 = tag, $3 = os, $4 = arch, $5 = work dir
install_binary() {
  bin="$1"
  archive="${bin}_${2#v}_${3}_${4}.tar.gz"
  dir="$5/$bin"
  mkdir -p "$dir"

  info "extracting $archive"
  tar -xzf "$5/$archive" -C "$dir"

  if [ ! -f "$dir/$bin" ]; then
    error "expected '$bin' inside $archive but it was not found"
  fi

  mkdir -p "$BIN_DIR"
  install -m 0755 "$dir/$bin" "$BIN_DIR/$bin" 2>/dev/null \
    || { cp "$dir/$bin" "$BIN_DIR/$bin" && chmod 0755 "$BIN_DIR/$bin"; }

  info "installed $bin -> $BIN_DIR/$bin"
  case "$bin" in
    rig | shiprig | changerig | clauderig)
      if ! "$BIN_DIR/$bin" completion install >/dev/null; then
        warn "could not install $bin zsh completion; run '$bin completion install' after updating"
      fi
      ;;
  esac
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

  target="${1:-all}"
  case "$target" in
    rig | shiprig | clauderig | codexrig | changerig | brewrig | all) ;;
    *) error "unknown binary '$target' (expected: rig, shiprig, clauderig, codexrig, changerig, brewrig, or omit for all)" ;;
  esac

  os="$(detect_os)"
  arch="$(detect_arch)"
  tag="$(resolve_version)"

  # brewrig only exists where Homebrew does. Asked for by name on Windows that
  # is an error worth stating; under `all` it is simply skipped, so one missing
  # platform does not fail the whole install.
  if [ "$target" = "brewrig" ] && [ "$os" = "windows" ]; then
    error "brewrig is darwin/linux only — Homebrew does not run on Windows"
  fi

  case "$target" in
    all)
      bins="rig shiprig clauderig codexrig changerig"
      [ "$os" = "windows" ] || bins="$bins brewrig"
      ;;
    *) bins="$target" ;;
  esac

  pick_sha256
  work="$(mktemp -d)"
  trap 'rm -rf "$work"' EXIT

  # Everything is downloaded and checked before anything is installed, so a
  # tampered or missing file leaves the existing tools as they were.
  fetch_checksums "$tag" "$work"
  for bin in $bins; do
    download_binary "$bin" "$tag" "$os" "$arch" "$work"
  done
  info "every archive matches the release's checksums"
  for bin in $bins; do
    install_binary "$bin" "$tag" "$os" "$arch" "$work"
  done

  path_hint
  info "done."
}

main "$@"
