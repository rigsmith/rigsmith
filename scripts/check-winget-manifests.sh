#!/bin/sh
# Verify the winget manifests GoReleaser generated before a moderator does.
#
# A winget package whose installers are not `NestedInstallerType: portable`
# fails winget's unattended-install validation — the zip is unpacked but nothing
# lands on PATH. That is not reported by a pipeline you can watch: it surfaces
# days later as a moderator asking "Is this a Portable package?" (see
# microsoft/winget-pkgs#403084, which cost 23 days for exactly this). These CLIs
# are all portable zips, so the check is absolute.
#
# The keys live *inside* each entry of the `Installers:` list, indented:
#
#   Installers:
#     - Architecture: arm64
#       NestedInstallerType: portable
#       NestedInstallerFiles:
#         - RelativeFilePath: rig.exe
#           PortableCommandAlias: rig
#
# Every installer is checked, not just the first: a manifest with x64 portable
# and arm64 not is broken for half its users and would look fine to a spot check.
#
#   $1  dist directory (default: dist)
#
# Exits non-zero when a generated manifest is wrong. Finding no manifests is a
# warning, not a failure: the winget pipe only runs on a real release (a
# --snapshot dry run skips every publisher), so an empty scan is the normal
# outcome everywhere else.
set -eu

dist="${1:-dist}"

manifests=$(find "$dist" -name '*.installer.yaml' 2>/dev/null | sort || true)
if [ -z "$manifests" ]; then
  echo "winget check: no installer manifests under $dist/ — nothing to verify (expected outside a real release)."
  exit 0
fi

fail=0
count=0
for m in $manifests; do
  count=$((count + 1))
  name=$(basename "$m")

  # Count per installer entry. Leading whitespace is expected — anchoring these
  # to column 0 is what made this check fail every correct manifest on its first
  # live run, blocking the rest of the release.
  installers=$(grep -cE '^[[:space:]]*-[[:space:]]+Architecture:' "$m" || true)
  portable=$(grep -cE '^[[:space:]]*NestedInstallerType:[[:space:]]*portable$' "$m" || true)
  aliases=$(grep -cE '^[[:space:]]*PortableCommandAlias:' "$m" || true)

  if [ "$installers" -eq 0 ]; then
    echo "::error::$name declares no installers — nothing would be published."
    fail=1
    continue
  fi
  if [ "$portable" -ne "$installers" ]; then
    echo "::error::$name: $portable of $installers installer(s) are NestedInstallerType: portable — winget's unattended install will fail for the rest."
    fail=1
    continue
  fi
  if [ "$aliases" -lt "$installers" ]; then
    echo "::error::$name: $aliases command alias(es) across $installers installer(s) — a command would not land on PATH."
    fail=1
    continue
  fi

  echo "winget check: $name — $installers installer(s), all portable, $aliases alias(es)"
done

if [ "$fail" -ne 0 ]; then
  echo "::error::winget manifests failed verification — fix them before the PRs reach a moderator."
  exit 1
fi

echo "winget check: $count manifest(s) verified portable."
