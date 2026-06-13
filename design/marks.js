// RigSmith mark system — single source of truth.
// Every mark = the [ ] rig bracket (constant) + an interior glyph (per tool).
// Rendered monochrome in one `ink` color so it works on any background.

const RS_PALETTE = {
  ink:        "#0E0E12", // near-black, cool
  paper:      "#ECECEE", // off-white
  muted:      "#6E6E78",
  rig:        "oklch(0.70 0.15 250)", // blue   — core
  change:     "oklch(0.70 0.15 300)", // violet
  ship:       "oklch(0.70 0.15 150)", // green
  claude:     "oklch(0.70 0.15 55)",  // amber
};

// --- bracket (constant) ---
function rsBrackets(ink, w = 8) {
  return `<path d="M38 21 L23 21 L23 79 L38 79" fill="none" stroke="${ink}" stroke-width="${w}" stroke-linecap="round" stroke-linejoin="round"/>`
       + `<path d="M62 21 L77 21 L77 79 L62 79" fill="none" stroke="${ink}" stroke-width="${w}" stroke-linecap="round" stroke-linejoin="round"/>`;
}

// --- interior glyphs ---
const RS_GLYPH = {
  // rig — the node (convention-first dev launcher)
  rig: ink => `<circle cx="50" cy="50" r="8.5" fill="${ink}"/>`,
  // changeRig — swap (two-arrow cycle: changeset lifecycle)
  change: ink => {
    const s = `fill="none" stroke="${ink}" stroke-width="6.5" stroke-linecap="round" stroke-linejoin="round"`;
    return `<path d="M37 44 A16 16 0 0 1 65 41" ${s}/><path d="M65 41 L66 33 M65 41 L57 42" ${s}/>`
         + `<path d="M63 56 A16 16 0 0 1 35 59" ${s}/><path d="M35 59 L34 67 M35 59 L43 58" ${s}/>`;
  },
  // shipRig — release (up arrow: publish/tag/launch)
  ship: ink => {
    const s = `fill="none" stroke="${ink}" stroke-width="7" stroke-linecap="round" stroke-linejoin="round"`;
    return `<path d="M50 63 L50 38 M41 47 L50 37 L59 47" ${s}/>`;
  },
  // claudeRig — spark (8-ray burst)
  claude: ink => {
    const s = `stroke="${ink}" stroke-width="6" stroke-linecap="round"`;
    return `<g ${s}><line x1="50" y1="36" x2="50" y2="64"/><line x1="36" y1="50" x2="64" y2="50"/><line x1="40" y1="40" x2="60" y2="60"/><line x1="60" y1="40" x2="40" y2="60"/></g>`;
  },
};

// Build a full mark. variant: 'rig'|'change'|'ship'|'claude'|'smith'
// Full color: `ink` colors the whole mark (brackets + glyph). The split
// opts (bracketInk/glyphInk) exist only to render the "don't" counter-example.
function rsMark(variant, ink, opts = {}) {
  const v = variant === "smith" ? "rig" : variant;
  const bracketInk = opts.bracketInk || ink;
  const glyphInk = opts.glyphInk || ink;
  const bw = opts.bracketWidth || 8;
  return rsBrackets(bracketInk, bw) + RS_GLYPH[v](glyphInk);
}

// Wrap inner svg content into a full <svg> string (for export / img).
function rsSvg(inner, opts = {}) {
  const size = opts.size || 100;
  const bg = opts.bg ? `<rect width="100" height="100" rx="${opts.radius ?? 0}" fill="${opts.bg}"/>` : "";
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" width="${size}" height="${size}">${bg}${inner}</svg>`;
}

if (typeof window !== "undefined") {
  Object.assign(window, { RS_PALETTE, rsBrackets, RS_GLYPH, rsMark, rsSvg });
}
