// Account monograms, shared by every window that names an account.
//
// Extracted rather than copied: the letter a badge shows depends on the OTHER
// accounts it is seen beside — "BR" and "BU" only differ because both exist —
// so two windows computing it separately from different label sets would show
// the same account under two different letters. One definition, one answer.

// Account labels are emails, and on one person's machine they are routinely the
// same person at different organisations — john@brightshore.io beside
// john@relatecpa.com — so an initial taken from the address would read "J" for
// both. The letter comes from the domain instead, which is the part that
// actually names the account.
//
// Where two accounts share a domain the local part stands in, and where the
// chosen source still collides the prefix grows a letter at a time: "BR" and
// "BU", never a letter plucked from the middle of a word, which reads as a typo
// rather than as an abbreviation.
//
// Computed over every label seen so far rather than the rows currently on
// screen, so filtering the list can't quietly re-letter a badge.
const seenAccounts = new Set();
let monoCache = new Map();

export function monogramFor(label) {
  if (!label) return '';
  if (!seenAccounts.has(label)) {
    seenAccounts.add(label);
    monoCache = buildMonograms([...seenAccounts]);
  }
  return monoCache.get(label) || '';
}

// The badge takes its colour from its own letter — b is blue, r is red, g is
// green — so the mapping needs no learning and no legend. Letters with no
// colour that starts the same way get one of the hues left over; those pairings
// are arbitrary and only have to stay stable and distinct.
//
// Tailwind's 400 step throughout: saturated enough to read as a colour at 22px
// on near-black, light enough not to vibrate against it.
const LETTER_HUE = {
  a: '#fbbf24', // amber
  b: '#60a5fa', // blue
  c: '#22d3ee', // cyan
  d: '#fb7185', // rose
  e: '#34d399', // emerald
  f: '#e879f9', // fuchsia
  g: '#4ade80', // green
  h: '#f472b6', // pink
  i: '#818cf8', // indigo
  j: '#94a3b8', // slate
  k: '#a8a29e', // stone
  l: '#a3e635', // lime
  m: '#9ca3af', // gray
  n: '#a3a3a3', // neutral
  o: '#fb923c', // orange
  p: '#c084fc', // purple
  q: '#fbbf24', // amber
  r: '#f87171', // red
  s: '#38bdf8', // sky
  t: '#2dd4bf', // teal
  u: '#22d3ee', // cyan
  v: '#a78bfa', // violet
  w: '#a78bfa', // violet
  x: '#a3e635', // lime
  y: '#facc15', // yellow
  z: '#a1a1aa', // zinc
};

export function hueFor(mono) {
  return LETTER_HUE[(mono || '').charAt(0).toLowerCase()] || null;
}

// Ring and letter in the hue, with the faintest wash behind it. A solid fill
// would make the badge the loudest thing in a row whose point is the title.
export function paintMono(badge, mono) {
  const hue = hueFor(mono);
  if (!hue) return badge;
  badge.style.color = hue;
  badge.style.borderColor = hue;
  badge.style.background = hue + '1a';
  return badge;
}

export function buildMonograms(labels) {
  const domain = l => {
    const at = l.lastIndexOf('@');
    return at >= 0 ? l.slice(at + 1) : '';
  };
  const clean = t => t.replace(/[^a-z0-9]/gi, '');
  const source = l => {
    const host = clean((domain(l).split('.')[0] || ''));
    // A shared domain says nothing about which account this is; the address does.
    const shared = host && labels.some(o => o !== l && clean((domain(o).split('.')[0] || '')) === host);
    if (host && !shared) return host;
    const at = l.lastIndexOf('@');
    return clean(at >= 0 ? l.slice(0, at) : l) || host || clean(l);
  };
  const out = new Map();
  for (const l of labels) {
    const src = source(l);
    if (!src) { out.set(l, '?'); continue; }
    let n = 1;
    while (n < src.length && labels.some(o => o !== l &&
      source(o).slice(0, n).toLowerCase() === src.slice(0, n).toLowerCase())) n++;
    out.set(l, src.slice(0, n).toUpperCase());
  }
  return out;
}
