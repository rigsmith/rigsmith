#!/usr/bin/env sh
# Generate Windows resource (.syso) files for each CLI: the RigSmith tile icon
# plus version-info metadata (product name, description, company, copyright, and
# the release version). The Go linker auto-links cmd/<tool>/rsrc_windows_*.syso
# into the matching Windows target and ignores it for linux/darwin, so this is a
# no-op for non-Windows builds.
#
# Pure Go — no system deps. Pinned go-winres version for reproducibility. Run
# from goreleaser's `before.hooks` (every tagged release embeds icons), or by
# hand: `sh ./scripts/winres.sh`.
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

for tool in rig shiprig changerig clauderig; do
  # Run from build/winres so the JSON's relative icon path (../icons/<tool>.png)
  # resolves identically regardless of go-winres's path-base; --out is absolute.
  (
    cd "$ROOT/build/winres"
    go run "$WINRES" make \
      --in "$tool.json" \
      --out "$ROOT/cmd/$tool/rsrc" \
      --arch amd64,arm64 \
      --product-version=git-tag \
      --file-version=git-tag
  )
done
