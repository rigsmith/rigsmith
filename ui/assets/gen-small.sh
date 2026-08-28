#!/bin/sh
# Regenerate the 32px Windows tray icons from the claudeRig mark, drawn heavier.
#
# The mark at its published weights is built for the macOS menu bar's 44px slot.
# Windows asks GetSystemMetrics(SM_CXSMICON) — 16px at 100% DPI — and stroke-width
# 8 on a 100 viewBox is 1.3px there, which anti-aliases into a smudge. Same
# geometry and same colours, thicker strokes, so it survives the size it is
# actually drawn at.
#
# Colours are the ones the existing 44px icons use, sampled rather than guessed.
set -eu
cd "$(dirname "$0")"
command -v rsvg-convert >/dev/null || { echo "needs rsvg-convert (brew install librsvg)" >&2; exit 1; }

mark() { # $1 = colour
  cat <<SVG
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" width="100" height="100">
<path d="M38 21 L23 21 L23 79 L38 79" fill="none" stroke="$1" stroke-width="15"
      stroke-linecap="round" stroke-linejoin="round"/>
<path d="M62 21 L77 21 L77 79 L62 79" fill="none" stroke="$1" stroke-width="15"
      stroke-linecap="round" stroke-linejoin="round"/>
<circle cx="50" cy="50" r="11" fill="$1"/>
</svg>
SVG
}

# The spark is a dot at this size. Its four crossed arms span ±14 of a 100 box —
# under three pixels at 16 — so they merge into a blob that reads as neither a
# spark nor anything else. A dot is what the spark looks like when it is too
# small to be a spark, which is the honest reduction.
set -- green:007329:4CB86A amber:9D7200:E4B750 red:B63132:EF6661
for spec in "$@"; do
  name=${spec%%:*}; rest=${spec#*:}; lightC=${rest%%:*}; darkC=${rest#*:}
  mark "#$lightC" | rsvg-convert -w 32 -h 32 -o "tray-$name-light-32.png"
  mark "#$darkC"  | rsvg-convert -w 32 -h 32 -o "tray-$name-dark-32.png"
done
echo "regenerated 6 × 32px tray icons"
