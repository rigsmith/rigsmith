#!/bin/sh
#
# site-installers.sh — fetch the installers rigsmith.sh serves, from the latest
# published release (netlify.toml's build command runs this).
#
#     site-installers.sh <public dir>
#
# `curl rigsmith.sh | sh` and `irm rigsmith.sh | iex` serve install.sh and
# install.ps1 from the site. They come from the latest release, not from main:
# a script on main can expect what no release has published yet (signed
# checksums, a new asset), and would refuse or fail on the release it installs.
# Taking the release's own script keeps the two in step.
#
# Each script is the release's attached asset, checked against the release's
# checksums.txt, or, for a release from before installers were attached, the
# file at the release's tag. Anything that can't be read fails the build: an
# old site is better than one serving a script from the wrong place.
#
# Env:
#     RIGSMITH_REPO   GitHub repo (default: rigsmith/rigsmith)
#
set -eu

REPO="${RIGSMITH_REPO:-rigsmith/rigsmith}"
OUT="${1:?usage: site-installers.sh <public dir>}"

fail() { printf 'site-installers: %s\n' "$1" >&2; exit 1; }

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
  else openssl dgst -sha256 -r "$1" | awk '{print $1}'
  fi
}

# The latest release's tag, from GitHub's /releases/latest redirect (no API
# call, so no API rate limit on a shared build host).
url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest")" \
  || fail "could not reach https://github.com/$REPO/releases/latest to find the latest release"
tag="${url##*/tag/}"
if [ -z "$tag" ] || [ "$tag" = "$url" ]; then
  fail "could not read the latest release's tag from $url"
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
base="https://github.com/$REPO/releases/download/$tag"

have_sums=0
if curl -fsSL "$base/checksums.txt" -o "$work/checksums.txt"; then
  have_sums=1
fi

for script in install.sh install.ps1; do
  if curl -fsSL "$base/$script" -o "$work/$script"; then
    # An attached installer is one the checksums vouch for.
    [ "$have_sums" = 1 ] || fail "$tag attaches $script but has no checksums.txt to check it against"
    want="$(awk -v n="$script" '$2 == n || $2 == "*" n { print $1; exit }' "$work/checksums.txt")"
    [ -n "$want" ] || fail "$tag attaches $script but its checksums.txt doesn't list it"
    got="$(sha256_of "$work/$script")"
    [ "$got" = "$want" ] || fail "$tag's $script does not match its checksums.txt (expected $want, got $got)"
    echo "site-installers: $script from $tag's release assets (checksum verified)"
  else
    curl -fsSL "https://raw.githubusercontent.com/$REPO/$tag/scripts/$script" -o "$work/$script" \
      || fail "could not read $script from $tag (not attached to the release, nor at scripts/$script in the tag)"
    echo "site-installers: $script from $tag's tag (the release predates attached installers)"
  fi
done

mkdir -p "$OUT"
cp "$work/install.sh" "$work/install.ps1" "$OUT/"
