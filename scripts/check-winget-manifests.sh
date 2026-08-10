#!/bin/sh
# Verify the winget manifests GoReleaser generated before a moderator does.
#
# A winget package whose installer manifest is not `NestedInstallerType: portable`
# fails winget's unattended-install validation — the zip is unpacked but nothing
# lands on PATH. That is not reported by a pipeline you can watch: it surfaces
# days later as a moderator asking "Is this a Portable package?" (see
# microsoft/winget-pkgs#403084, which cost 23 days for exactly this). These CLIs
# are all portable zips, so the check is absolute: every generated manifest must
# say portable and give every binary an alias.
#
#   $1  dist directory (default: dist)
#
# Exits non-zero when a generated manifest is wrong. Finding no manifests is a
# warning, not a failure: the winget pipe only runs on a real release (a
# --snapshot dry run skips every publisher), so an empty scan is the normal
# outcome everywhere else.
set -eu

dist="${1:-dist}"

manifests=$(find "$dist" -name '*.installer.yaml' 2>/dev/null || true)
if [ -z "$manifests" ]; then
  echo "winget check: no installer manifests under $dist/ — nothing to verify (expected outside a real release)."
  exit 0
fi

fail=0
count=0
for m in $manifests; do
  count=$((count + 1))
  name=$(basename "$m")

  if ! grep -q '^NestedInstallerType: portable$' "$m"; then
    got=$(grep -E '^(Nested)?InstallerType:' "$m" | tr '\n' ' ' || true)
    echo "::error::$name is not a portable package — winget's unattended install will fail. Got: ${got:-none}"
    fail=1
    continue
  fi
  if ! grep -q 'PortableCommandAlias:' "$m"; then
    echo "::error::$name declares no PortableCommandAlias — the command would not land on PATH."
    fail=1
    continue
  fi

  aliases=$(grep -c 'PortableCommandAlias:' "$m" || true)
  echo "winget check: $name — portable, $aliases command alias(es)"
done

if [ "$fail" -ne 0 ]; then
  echo "::error::winget manifests failed verification — fix them before the PRs reach a moderator."
  exit 1
fi

echo "winget check: $count manifest(s) verified portable."
