#!/bin/sh
# Submit a released version to winget via komac: generate → correct → verify →
# submit.
#
#   sh scripts/winget-submit.sh 1.5.1              # generate + verify, submit nothing
#   sh scripts/winget-submit.sh 1.5.1 --submit     # open the PRs
#
# Why komac rather than GoReleaser's winget publisher, which we used for 1.5.0
# and 1.5.1: komac updates the *published* manifest, carrying forward everything
# the package already declares and rewriting only version, URLs and hashes.
# GoReleaser writes a manifest from its own config, so anything it has no field
# for is simply dropped. That cost us on all five 1.5.1 submissions:
#
#   Missing property PublisherSupportUrl / Copyright / Tags / ReleaseNotes /
#   ReleaseNotesUrl / Commands / NestedInstallerType / NestedInstallerFiles
#
# `Commands` has no answer in GoReleaser at all — no config field exists — and it
# is what `winget search` and `winget install --command` read. `Moniker`
# regressed the same way (changerig -> ChangeRig), being derived from the
# package name with no override.
#
# The three-step shape matters. komac can write manifests to a directory
# (--dry-run --output) and submit a directory separately (komac submit), so the
# check below runs BEFORE anything reaches winget-pkgs. Every earlier version of
# this check could only run after GoReleaser had already opened the PRs.
#
# komac needs a GitHub token with `public_repo` (GITHUB_TOKEN or --token).
set -eu

version="${1:?usage: winget-submit.sh <version> [--submit]}"
submit="${2:-}"
out="${OUTPUT_DIR:-dist/winget}"
base="https://github.com/rigsmith/rigsmith/releases/download/v${version}"

# identifier:archive-prefix. The bundle's archive is named for the repo rather
# than the package, so one cannot be derived from the other.
packages="RigSmith.Rig:rig
RigSmith.ShipRig:shiprig
RigSmith.ChangeRig:changerig
RigSmith.ClaudeRig:clauderig
RigSmith.Rigsmith:rigsmith"

rm -rf "$out"
mkdir -p "$out"

for entry in $packages; do
  id=${entry%%:*}
  prefix=${entry#*:}
  echo "→ generating $id $version"
  komac update "$id" --version "$version" \
    --urls "${base}/${prefix}_${version}_windows_amd64.zip" \
           "${base}/${prefix}_${version}_windows_arm64.zip" \
    --output "$out" \
    --release-notes-url "https://github.com/rigsmith/rigsmith/releases/tag/v${version}" \
    --dry-run >/dev/null
done

# komac ANALYSES each installer rather than trusting the published manifest, and
# it reads clauderig.exe as an installer: it emits `NestedInstallerType: exe`
# for that one package while getting the other four right. This is not new — the
# same misdetection shipped in ClaudeRig 1.4.0 and took 23 days to come back as
# a moderator asking "Is this a Portable package?".
#
# Every package here is a single static Go binary in a zip, so `exe` is never
# correct for any of them, and the correction is announced rather than silent.
# perl, not sed: these files are CRLF and the line ending must survive.
for m in $(find "$out" -name '*.installer.yaml' | sort); do
  if grep -qE '^NestedInstallerType:[[:space:]]*exe' "$m"; then
    echo "  ! $(basename "$m"): komac detected \`exe\`; correcting to \`portable\` (every rigsmith package is a portable CLI)"
    perl -pi -e 's/^(NestedInstallerType:[ \t]*)exe([ \t]*\r?)$/${1}portable$2/' "$m"
  fi
done

# Verify BEFORE submitting — the whole point of generating to a directory first.
echo
sh "$(dirname "$0")/check-winget-manifests.sh" "$out"

if [ "$submit" != "--submit" ]; then
  echo
  echo "Nothing submitted. Manifests are under $out/ — re-run with --submit to open the PRs."
  exit 0
fi

echo
echo "→ submitting $out"
komac submit "$out" --all --yes
