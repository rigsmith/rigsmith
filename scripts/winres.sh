#!/usr/bin/env sh
# Generate Windows resource (.syso) files for each CLI: the RigSmith tile icon
# plus version-info metadata (product name, description, company, copyright, and
# the release version). The Go linker auto-links cmd/<tool>/rsrc_windows_*.syso
# into the matching Windows target and ignores it for linux/darwin, so this is a
# no-op for non-Windows builds.
#
# Pure Go — no system deps. Pinned go-winres version for reproducibility. Run
# from goreleaser's `before.hooks` (every tagged release embeds icons), or by
# hand:
#
#   sh ./scripts/winres.sh        # the CLIs
#   sh ./scripts/winres.sh ui     # the claudeRig UI
#   sh ./scripts/winres.sh all    # both
#
# The window is a target of its own because it releases on its own tag: its
# version comes from its module rather than from `git describe`, and its .syso
# lands beside its main package in ui/ rather than under cmd/.
#
# Icon source: build/icons/<tool>.png (256px, rasterized from design/marks/
# tile-*.svg via rsvg-convert — see build/icons/README.md). Each tool's
# resources are described by build/winres/<tool>.json.
set -e

# Pin go-winres; bump deliberately. https://github.com/tc-hib/go-winres
WINRES="github.com/tc-hib/go-winres@v0.3.3"

# Resolve the repo root from this script's own location, so it works from any
# cwd — goreleaser runs hooks at the repo root, but a manual run may not.
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

target="${1:-cli}"

# make <json-basename> <out-prefix> <version>
#
# Run from build/winres so the JSON's relative icon path (../icons/<tool>.png)
# resolves identically regardless of go-winres's path-base; --out is absolute.
make_syso() {
  (
    cd "$ROOT/build/winres"
    go run "$WINRES" make \
      --in "$1.json" \
      --out "$2" \
      --arch amd64,arm64 \
      --product-version="$3" \
      --file-version="$3"
  )
}

case "$target" in
  cli|ui|all) ;;
  *) echo "usage: winres.sh [cli|ui|all]" >&2; exit 1 ;;
esac

if [ "$target" = cli ] || [ "$target" = all ]; then
  for tool in rig shiprig changerig clauderig; do
    make_syso "$tool" "$ROOT/cmd/$tool/rsrc" git-tag
  done
fi

if [ "$target" = ui ] || [ "$target" = all ]; then
  # NOT git-tag. `git describe` answers with whatever tag is newest in this
  # history — the CLIs' vX.Y.Z as often as not — and the window would then
  # report one version in `main.version` and another in the properties dialog
  # Windows shows for the same file. UI_VERSION when the release sets it, the
  # module's own declaration otherwise.
  version="${UI_VERSION:-$(sh "$ROOT/scripts/ui-version.sh" "$ROOT/ui/go.mod")}"
  make_syso clauderigUi "$ROOT/ui/rsrc" "$version"
fi
