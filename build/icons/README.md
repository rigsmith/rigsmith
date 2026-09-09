# Windows .exe icons

256×256 PNGs embedded into each CLI's Windows `.exe` as its application icon
(plus version-info metadata) at release time. macOS/Linux binaries are
unaffected.

## Source

Rasterized from the square tile marks in [`design/marks/`](../../design/marks/)
(`tile-rig.svg`, `tile-shipRig.svg`, `tile-changeRig.svg`, `tile-claudeRig.svg`)
— the tile treatment carries the rounded dark background an app icon needs, vs.
the transparent plain marks. Committed here so CI needs no SVG rasterizer.

## Regenerate

Requires `rsvg-convert` (`brew install librsvg`):

```sh
for pair in rig:tile-rig shiprig:tile-shipRig changerig:tile-changeRig \
            clauderig:tile-claudeRig claudeRigUi:tile-claudeRig; do
  tool=${pair%%:*}; src=${pair##*:}
  rsvg-convert -w 256 -h 256 "design/marks/$src.svg" -o "build/icons/$tool.png"
done
```

`claudeRigUi` reuses the claudeRig tile deliberately: it is the claudeRig app,
and its macOS bundle is built from the same mark (`design/marks/png/app-claudeRig-512.png`,
see `scripts/package-ui.sh`). One window, one icon, whichever platform it is on.

## How they reach the .exe

`scripts/winres.sh` (run from goreleaser's `before` hook) feeds each PNG +
`build/winres/<tool>.json` through [go-winres](https://github.com/tc-hib/go-winres),
emitting `cmd/<tool>/rsrc_windows_*.syso` — or `ui/rsrc_windows_*.syso` for the
window, whose main package is not under `cmd/` and whose version comes from its
own module rather than from `git describe` (`sh scripts/winres.sh ui`). The Go linker embeds it into the
Windows build automatically. go-winres downsamples the 256px source to the
smaller icon sizes (48/32/16) itself.
