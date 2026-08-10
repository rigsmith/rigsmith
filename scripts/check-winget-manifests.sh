#!/bin/sh
# Verify winget installer manifests before a moderator does.
#
# A package whose installers are not `NestedInstallerType: portable` fails
# winget's unattended-install validation — the zip is unpacked but nothing lands
# on PATH. No pipeline we own reports that: it surfaces days later as a moderator
# asking "Is this a Portable package?" (microsoft/winget-pkgs#403084, 23 days).
#
# The keys appear in either of two valid places, and this check has now been
# wrong about each of them in turn, so both are handled explicitly:
#
#   root-level (komac, inherited from the published version) — applies to every
#   installer, and NestedInstallerFiles is declared once:
#     NestedInstallerType: portable
#     NestedInstallerFiles:
#     - RelativeFilePath: changerig.exe
#       PortableCommandAlias: changerig
#     Installers:
#     - Architecture: x64
#
#   per-installer (GoReleaser) — repeated inside each Installers: entry:
#     Installers:
#       - Architecture: arm64
#         NestedInstallerType: portable
#         NestedInstallerFiles:
#           - RelativeFilePath: rig.exe
#             PortableCommandAlias: rig
#
# What is actually required, independent of shape:
#   - every installer is covered by a portable declaration;
#   - no declaration says anything other than portable;
#   - every nested file has a command alias, or that binary lands nowhere;
#   - Commands is present — winget search / `--command` use it, and losing it is
#     the metadata regression that put Manifest-Metadata-Consistency on all five
#     1.5.1 submissions.
#
#   $1  directory to scan (default: dist)
#
# Exits non-zero when a manifest is wrong. Finding none is a warning, not a
# failure — most runs generate no manifests at all.
set -eu

dir="${1:-dist}"

manifests=$(find "$dir" -name '*.installer.yaml' 2>/dev/null | sort || true)
if [ -z "$manifests" ]; then
  echo "winget check: no installer manifests under $dir/ — nothing to verify (expected outside a real release)."
  exit 0
fi

fail=0
count=0
norm=$(mktemp)
trap 'rm -f "$norm"' EXIT

for m in $manifests; do
  count=$((count + 1))
  name=$(basename "$m")

  # winget-pkgs manifests are CRLF by convention — komac writes them that way,
  # GoReleaser writes LF. A trailing \r defeats every `$`-anchored pattern below,
  # which silently turned "portable" into "not portable". Match on normalized
  # bytes rather than assuming either convention.
  tr -d '\r' < "$m" > "$norm"

  installers=$(grep -cE '^[[:space:]]*-[[:space:]]+Architecture:' "$norm" || true)
  root_portable=$(grep -cE '^NestedInstallerType:[[:space:]]*portable$' "$norm" || true)
  nested_portable=$(grep -cE '^[[:space:]]+NestedInstallerType:[[:space:]]*portable$' "$norm" || true)
  wrong_type=$(grep -E '^[[:space:]]*NestedInstallerType:' "$norm" | grep -vcE ':[[:space:]]*portable$' || true)
  files=$(grep -cE '^[[:space:]]*-?[[:space:]]*RelativeFilePath:' "$norm" || true)
  aliases=$(grep -cE '^[[:space:]]*PortableCommandAlias:' "$norm" || true)
  commands=$(grep -cE '^Commands:' "$norm" || true)

  if [ "$installers" -eq 0 ]; then
    echo "::error::$name declares no installers — nothing would be published."
    fail=1
    continue
  fi
  if [ "$wrong_type" -ne 0 ]; then
    got=$(grep -E '^[[:space:]]*NestedInstallerType:' "$norm" | grep -vE ':[[:space:]]*portable$' | tr -d ' ' | tr '\n' ' ')
    echo "::error::$name declares a non-portable installer type ($got) — winget's unattended install will fail."
    fail=1
    continue
  fi
  # Root-level covers every installer; otherwise each installer needs its own.
  if [ "$root_portable" -eq 0 ] && [ "$nested_portable" -ne "$installers" ]; then
    echo "::error::$name: $nested_portable of $installers installer(s) declare NestedInstallerType: portable, and none is declared at the root."
    fail=1
    continue
  fi
  if [ "$files" -eq 0 ]; then
    echo "::error::$name lists no NestedInstallerFiles — nothing would be extracted."
    fail=1
    continue
  fi
  if [ "$aliases" -ne "$files" ]; then
    echo "::error::$name: $aliases command alias(es) for $files nested file(s) — a binary would not land on PATH."
    fail=1
    continue
  fi
  if [ "$commands" -eq 0 ]; then
    echo "::error::$name has no Commands — winget search and \`--command\` lose the package's commands (metadata regression)."
    fail=1
    continue
  fi

  shape="root-level"
  [ "$root_portable" -eq 0 ] && shape="per-installer"
  echo "winget check: $name — $installers installer(s), portable ($shape), $aliases alias(es), Commands present"
done

if [ "$fail" -ne 0 ]; then
  echo "::error::winget manifests failed verification — fix them before the PRs reach a moderator."
  exit 1
fi

echo "winget check: $count manifest(s) verified."
