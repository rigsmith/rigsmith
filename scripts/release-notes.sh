#!/bin/sh
# Print one version's entry from a changelog: the lines under `## <version>`,
# up to the next `## ` heading. The GitHub release takes these as its notes, so
# the release page says what the changesets say rather than listing commits.
#
#   scripts/release-notes.sh <version> [CHANGELOG.md]
#
# Fails when the changelog has no entry for the version, or an empty one: a
# release must not go out with blank notes because a heading moved.
set -eu

version="${1:?usage: release-notes.sh <version> [changelog]}"
changelog="${2:-CHANGELOG.md}"
version="${version#v}"

notes=$(awk -v want="## $version" '
  $0 == want { found = 1; next }
  found && /^## / { exit }
  found { print }
' "$changelog")

# Trim leading and trailing blank lines.
notes=$(printf '%s\n' "$notes" | sed -e '/./,$!d' | awk '{ lines[NR] = $0 } END { n = NR; while (n > 0 && lines[n] == "") n--; for (i = 1; i <= n; i++) print lines[i] }')

if [ -z "$notes" ]; then
  echo "release-notes.sh: no entry for $version in $changelog" >&2
  exit 1
fi
printf '%s\n' "$notes"
