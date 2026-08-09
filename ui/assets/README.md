# Tray icons

Six 44×44 PNGs — the three health states (green / amber / red) × the two menu
bar backgrounds (light / dark). `assets.go` embeds them; `Tray(health.Level)`
returns the matching pair for `SetIcon` / `SetDarkModeIcon`.

44px is the macOS retina menu bar slot (22pt @2x). Windows wants 16 or 32 and
downsamples from this cleanly.

## Source

The plain `claudeRig` mark from [`design/marks/`](../../design/marks/) —
brackets + spark — recoloured per state. The mark system draws every mark
"monochrome in one `ink` color so it works on any background"
([`design/marks.js`](../../design/marks.js)), which is exactly what a
health-coloured tray icon needs: the state rides the whole glyph, no tinting or
badging required.

Inks come from `core/brand` (`Green` = ok, `Yellow` = warn, `Red` = error), each
an `AdaptiveColor` whose Dark/Light values map to the dark/light menu bar:

| State | Dark bg | Light bg |
|---|---|---|
| green | `#4CB86A` | `#007329` |
| amber | `#E4B750` | `#9D7200` |
| red   | `#EF6661` | `#B63132` |

`brand.Yellow` (warn), not `brand.Amber` — amber is claudeRig's own accent, and
reusing it for a warning state would make "needs attention" look like branding.

Committed here so CI needs no SVG rasterizer, matching
[`build/icons/`](../../build/icons/README.md).

## Regenerate

Requires `rsvg-convert` (`brew install librsvg`). From the repo root:

```sh
for spec in green:4CB86A:007329 amber:E4B750:9D7200 red:EF6661:B63132; do
  state=${spec%%:*}; rest=${spec#*:}; dark=${rest%%:*}; light=${rest##*:}
  for mode in dark light; do
    hex=$dark; [ "$mode" = light ] && hex=$light
    sed "s/#E48233/#$hex/g" design/marks/claudeRig.svg > /tmp/mark.svg
    rsvg-convert -w 44 -h 44 /tmp/mark.svg -o "ui/assets/tray-$state-$mode.png"
  done
done
```

## Open: macOS template icons

macOS convention is a *template* icon — black + transparent, auto-adapting to
the menu bar. A template icon is monochrome by definition, so health colour
cannot ride the glyph under it.

These assets take the other branch deliberately: coloured, non-template, with
`SetDarkModeIcon` handling light/dark by hand. Ambient health signalling is the
tray's entire job here, and a monochrome mark cannot do it. The cost is that the
icon does not tint with macOS accent settings the way a template icon would.

Flagged in [the UI plan](../../docs/CLAUDERIG-UI-PLAN.md) as a Phase 1 decision;
this is the current answer, not a settled one.
