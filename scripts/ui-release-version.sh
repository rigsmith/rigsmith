#!/bin/sh
# The version a UI release is being cut as, checked against the module that
# declares it.
#
#   sh scripts/ui-release-version.sh ui/v0.2.0   # -> 0.2.0
#   sh scripts/ui-release-version.sh             # -> <module version>-dryrun
#
# The tag and ui/go.mod have to agree. They are written at different moments —
# changerig bumps the module, `shiprig tag` names the tag from it — and if they
# ever disagree the release would ship a window that reports one number under a
# tag that promises another, with the cask and the winget manifest each
# believing a different one. Cheaper to refuse here than to find out from a
# 404 in somebody's `brew upgrade`.
set -eu

# Resolved from this script's own location rather than the caller's directory:
# the workflow runs it from the repo root and `go test ./scripts` runs it from
# scripts/, and "which go.mod" must not depend on which.
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
mod=$(sh "$here/ui-version.sh" "$here/../ui/go.mod")

ref="${1:-}"
case "$ref" in
  "")
    # No tag: a manual dry run. Marked, so an artifact from one can never be
    # mistaken for a release build.
    printf '%s-dryrun\n' "$mod"
    exit 0
    ;;
  ui/v*) tag=${ref#ui/v} ;;
  *)
    echo "$ref is not a UI release tag — expected ui/vX.Y.Z" >&2
    exit 1
    ;;
esac

if [ "$tag" != "$mod" ]; then
  echo "tag $ref says $tag but ui/go.mod says $mod — one of them is wrong, and nothing here can tell which" >&2
  exit 1
fi
printf '%s\n' "$tag"
