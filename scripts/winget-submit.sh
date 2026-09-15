#!/bin/sh
# Submit a released version to winget via komac: generate → correct → verify →
# submit.
#
#   sh scripts/winget-submit.sh 1.5.1              # generate + verify, submit nothing
#   sh scripts/winget-submit.sh 1.5.1 --submit     # open the PRs
#
# Env:
#   WINGET_TAG       the release holding the archives (default v<version>)
#   WINGET_PACKAGES  identifier:archive-prefix lines (default: the four CLIs
#                    and the bundle). The claudeRig UI release passes its own.
#   OUTPUT_DIR       where manifests are generated (default dist/winget)
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

# WINGET_TAG is the release the archives live in. It defaults to the CLIs'
# convention, v<version>, because that is the case with four callers; the window
# overrides it, since it ships on its own tag (ui/vX.Y.Z) at its own version and
# neither number can be derived from the other.
tag="${WINGET_TAG:-v${version}}"
base="https://github.com/rigsmith/rigsmith/releases/download/${tag}"

# identifier:archive-prefix. The bundle's archive is named for the repo rather
# than the package, so one cannot be derived from the other.
#
# WINGET_PACKAGES overrides the set for a release that is not the CLIs'. Every
# rule below still applies to whatever is in it: each package is a single Go
# binary in a zip, named <prefix>_<version>_windows_<arch>.zip.
packages="${WINGET_PACKAGES:-RigSmith.Rig:rig
RigSmith.ShipRig:shiprig
RigSmith.ChangeRig:changerig
RigSmith.ClaudeRig:clauderig
RigSmith.CodexRig:codexrig
RigSmith.Rigsmith:rigsmith}"

rm -rf "$out"
mkdir -p "$out"

# A package winget has never seen has nothing to update, and komac exits 1 saying
# so. That is expected exactly once per tool — the first submission is a `komac
# new` done by hand (see docs/WINGET-SUBMISSIONS.md) — but under `set -e` it used
# to abort this script, and since the submission below is one call for the whole
# directory, the FIVE published packages went unsubmitted too. A new tool must not
# be able to hold back everything shipping beside it, so it is skipped and named.
#
# Only that one error is tolerated. Anything else still stops the run.
err=$(mktemp)
trap 'rm -f "$err"' EXIT
skipped=""

# komac's message is not evidence on its own. It reports the lookup with
# `.map_err(|_| GitHubError::PackageNonExistent(id))`, throwing the real error
# away, so a rate limit, a 5xx or a JSON failure all print "<id> does not exist
# in microsoft/winget-pkgs" exactly as an unpublished package does. Believing it
# blindly would turn a GitHub blip during a release into a silent no-op — every
# package "missing", nothing submitted, exit 0. That is the failure this script
# was changed to prevent, so absence gets confirmed against winget-pkgs itself.
#
# 404 is absent. 200 is published, so the komac failure was something else. Any
# other answer — 403 rate limit, 5xx, a curl that could not run — is not an
# answer, and none of them mean absent.
package_absent() {
  # Not `path`: zsh ties that name to PATH, and anyone sourcing or adapting this
  # under zsh would wipe their own. Cheap to avoid, miserable to debug.
  pkgpath=$(printf '%s' "$1" | tr '.' '/')
  letter=$(printf '%s' "$1" | cut -c1 | tr '[:upper:]' '[:lower:]')
  code=$(curl -sS -o /dev/null -w '%{http_code}' \
    ${GITHUB_TOKEN:+-H "Authorization: Bearer ${GITHUB_TOKEN}"} \
    -H "Accept: application/vnd.github+json" \
    "https://api.github.com/repos/microsoft/winget-pkgs/contents/manifests/${letter}/${pkgpath}" 2>/dev/null)
  if [ "$code" = "404" ]; then
    return 0
  fi
  echo "::warning::$1: komac said it does not exist, but winget-pkgs answered ${code:-no response} rather than 404. Treating that as a real failure rather than a new package."
  return 1
}

for entry in $packages; do
  id=${entry%%:*}
  prefix=${entry#*:}
  echo "→ generating $id $version"
  if komac update "$id" --version "$version" \
    --urls "${base}/${prefix}_${version}_windows_amd64.zip" \
           "${base}/${prefix}_${version}_windows_arm64.zip" \
    --output "$out" \
    --release-notes-url "https://github.com/rigsmith/rigsmith/releases/tag/${tag}" \
    --dry-run >/dev/null 2>"$err"; then
    continue
  fi
  if grep -q "does not exist in microsoft/winget-pkgs" "$err" && package_absent "$id"; then
    echo "::warning::$id is not published in winget-pkgs yet, so there is nothing to update — skipping it. Its first submission is a manual \`komac new\`; see docs/WINGET-SUBMISSIONS.md. Every published package still goes out."
    skipped="$skipped $id"
    continue
  fi
  cat "$err" >&2
  exit 1
done

if [ -n "$skipped" ]; then
  echo
  echo "Not submitted (never published):$skipped"
fi

# Every package was new, so there is nothing to update and nothing to verify.
# Not a failure: the release published its archives, and the manual `komac new`
# for each is what comes next.
if [ -z "$(find "$out" -name '*.installer.yaml' -print -quit)" ]; then
  echo "No published package to update. Nothing to submit."
  exit 0
fi

# A tripwire that should never fire. komac classifies a nested .exe by
# substring-matching its PE FileDescription/OriginalFilename against
# ["installer", "setup", "7zs.sfx", "7zsd.sfx"] — nothing else about the binary
# matters. clauderig's description used to read "Sync your Claude Code setup
# across machines", so komac called it an installer, winget unpacked the zip and
# put nothing on PATH, and it surfaced 23 days later as a moderator asking "Is
# this a Portable package?". The descriptions no longer contain any of those
# words (build/winres/, pinned by a test there), so this correction is now a
# regression detector: if it prints, a description has drifted back.
#
# Every package here is a single static Go binary in a zip, so `exe` is never
# correct for any of them, and the correction is announced rather than silent.
# perl, not sed: these files are CRLF and the line ending must survive.
for m in $(find "$out" -name '*.installer.yaml' | sort); do
  if grep -qE '^NestedInstallerType:[[:space:]]*exe' "$m"; then
    echo "::warning::$(basename "$m"): komac read this binary as an installer. Its PE FileDescription or OriginalFilename now contains one of installer/setup/7zs.sfx/7zsd.sfx — see build/winres/. Correcting to portable for this submission."
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
