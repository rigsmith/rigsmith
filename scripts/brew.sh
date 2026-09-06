#!/bin/sh
# Install rigsmith's tools with Homebrew, served at rigsmith.sh/brew.
#
# What this exists for is one line of it: the fully-qualified cask name. Homebrew
# refuses to load a cask from a non-official tap by its short name unless the tap
# has been trusted, so `brew install rig` stops with a trust prompt while
# `brew install rigsmith/tap/rig` just works. A URL people paste is exactly where
# that detail should be baked in rather than remembered.
#
#   curl -fsSL rigsmith.sh/brew | sh              # everything
#   curl -fsSL rigsmith.sh/brew/clauderig | sh    # one tool
#
# The edge function passes the selection as $1; see site/netlify/edge-functions.
set -eu

tap="rigsmith/tap"
what="${1:-rigsmith}"

if ! command -v brew >/dev/null 2>&1; then
  echo "Homebrew is not installed. See https://brew.sh, or install without it:" >&2
  echo "  curl -fsSL rigsmith.sh | sh" >&2
  exit 1
fi

# The tools ship as casks, and casks are macOS-only. Say so rather than letting
# brew fail three steps later with something about an unsupported platform.
if [ "$(uname -s)" != "Darwin" ]; then
  echo "The Homebrew packages are macOS casks. On Linux, install directly:" >&2
  echo "  curl -fsSL rigsmith.sh | sh" >&2
  exit 1
fi

case "$what" in
  all|rigsmith) cask="rigsmith" ;;                       # the bundle: all four CLIs
  rig|changerig|shiprig|clauderig|clauderig-ui) cask="$what" ;;
  *)
    echo "unknown package: $what" >&2
    echo "try: rigsmith (all four), rig, changerig, shiprig, clauderig, clauderig-ui" >&2
    exit 1
    ;;
esac

# Fully qualified on purpose — see the note at the top. `brew install` taps
# automatically for a qualified name, so there is no separate `brew tap` step.
echo "installing ${tap}/${cask}"
exec brew install --cask "${tap}/${cask}"
