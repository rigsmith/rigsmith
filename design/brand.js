// Populate the RigSmith brand page from marks.js
(function(){
  const P = RS_PALETTE;
  const COL = { rig:'var(--rig)', change:'var(--change)', ship:'var(--ship)', claude:'var(--claude)' };
  const $ = s => document.querySelector(s);
  const setSvg = (sel, inner) => { const el=$(sel); if(el) el.innerHTML = inner; };

  // hero + feature marks (two-tone: neutral brackets + accent glyph)
  setSvg('#hero-mark', rsMark('smith','var(--rig)'));
  setSvg('#feat-mark', rsMark('rig','var(--rig)'));
  setSvg('#lock-h', rsMark('smith','var(--rig)'));
  setSvg('#lock-v', rsMark('smith','var(--rig)'));

  // hero chips
  const tools = [
    {k:'rig',    name:['','rig'],        col:COL.rig},
    {k:'change', name:['change','Rig'],  col:COL.change},
    {k:'ship',   name:['ship','Rig'],     col:COL.ship},
    {k:'claude', name:['claude','Rig'],  col:COL.claude},
  ];
  $('#hero-chips').innerHTML = tools.map(t =>
    `<span class="chip"><svg viewBox="0 0 100 100">${rsMark(t.k, t.col)}</svg>${t.name[0]}<b>${t.name[1]}</b></span>`
  ).join('');

  // ---------- construction visuals ----------
  // anatomy: mark + guide grid + stroke callouts
  const guide = `
    <g stroke="#2a2a34" stroke-width="1">
      <line x1="23" y1="6" x2="23" y2="94"/><line x1="77" y1="6" x2="77" y2="94"/>
      <line x1="6" y1="21" x2="94" y2="21"/><line x1="6" y1="79" x2="94" y2="79"/>
      <line x1="50" y1="6" x2="50" y2="94"/><line x1="6" y1="50" x2="94" y2="50"/>
    </g>
    <circle cx="50" cy="50" r="20" fill="none" stroke="#2a2a34" stroke-width="1" stroke-dasharray="3 4"/>`;
  setSvg('#con-anatomy', guide + rsMark('rig', P.paper));

  // clearspace: mark inset with a dashed margin frame one arm-width out
  const clear = `
    <rect x="9" y="9" width="82" height="82" rx="8" fill="none" stroke="#2a2a34" stroke-width="1" stroke-dasharray="4 5"/>
    ${rsMark('rig', P.paper)}`;
  setSvg('#con-clear', clear);

  // min size: tiny mark centered
  setSvg('#con-min', `<g transform="translate(33 33) scale(0.34)">${rsMark('rig', P.paper)}</g>
    <text x="50" y="74" text-anchor="middle" fill="#52525c" font-size="6" font-family="JetBrains Mono">16px</text>`);

  // ---------- color swatches ----------
  const accents = [
    {key:'rig',    name:'Rig Blue',   role:'core · rig',     css:'--rig',    h:250},
    {key:'change', name:'Change',     role:'changeRig',      css:'--change', h:300},
    {key:'ship',   name:'Release',    role:'shipRig',        css:'--ship',   h:150},
    {key:'claude', name:'Claude',     role:'claudeRig',      css:'--claude', h:55},
  ];
  // read computed color -> hex via canvas (handles oklch reliably)
  const _c = document.createElement('canvas'); _c.width = _c.height = 1;
  const _ctx = _c.getContext('2d');
  function cssVarToHex(varName){
    const resolved = getComputedStyle(document.documentElement).getPropertyValue(varName).trim();
    _ctx.fillStyle = '#000';
    _ctx.fillStyle = resolved;
    _ctx.fillRect(0,0,1,1);
    const [r,g,b] = _ctx.getImageData(0,0,1,1).data;
    return '#'+[r,g,b].map(n=>n.toString(16).padStart(2,'0')).join('').toUpperCase();
  }
  $('#swatches').innerHTML = accents.map(a =>
    `<div class="sw">
       <div class="chip-c" style="background:var(${a.css})"><svg viewBox="0 0 100 100">${rsMark(a.key, P.ink)}</svg></div>
       <div class="meta">
         <div class="name">${a.name}</div>
         <div class="role">${a.role}</div>
         <div class="vals">
           <code>oklch(0.70 0.15 ${a.h})</code>
           <code data-hex="${a.css}">#······</code>
         </div>
       </div>
     </div>`
  ).join('');
  // fill hex
  document.querySelectorAll('[data-hex]').forEach(el=>{
    el.textContent = cssVarToHex(el.dataset.hex);
  });

  const neutrals=[
    {name:'Ink',   v:'#0E0E12', role:'background'},
    {name:'Paper', v:'#ECECEE', role:'foreground'},
    {name:'Muted', v:'#7B7B87', role:'secondary text'},
  ];
  $('#neutrals').innerHTML = neutrals.map(n=>
    `<div class="nsw"><div class="dot" style="background:${n.v}"></div>
      <div><div class="name">${n.name}</div><code>${n.v} · ${n.role}</code></div></div>`
  ).join('');

  // ---------- family cards ----------
  const fam=[
    {k:'rig',    pre:'',       suf:'rig',    col:'--rig',    role:'Convention-first dev launcher. Auto-detects your stack and runs the right native build, test, run and format — plus dev-loop verbs like coverage, doctor and kill.', tag:'glyph · node'},
    {k:'change', pre:'change', suf:'Rig',    col:'--change', role:'Lean changeset lifecycle: init → add → status → version. Bumps versions and writes the CHANGELOG with a dependency cascade. Aliased changeset.', tag:'glyph · cycle'},
    {k:'ship',   pre:'ship',   suf:'Rig',    col:'--ship',   role:'The release front door. Everything changeRig does, plus publish / tag / pre orchestration and a configurable release-step pipeline.', tag:'glyph · release'},
    {k:'claude', pre:'claude', suf:'Rig',    col:'--claude', role:'Syncs your Claude Code setup — config, skills, session history — across machines via a private git repo, with cross-OS path correction and secret stripping.', tag:'glyph · spark'},
  ];
  $('#fam').innerHTML = fam.map(f=>
    `<div class="fam-card">
       <div class="glow" style="background:var(${f.col})"></div>
       <svg viewBox="0 0 100 100">${rsMark(f.k, `var(${f.col})`)}</svg>
       <div class="fm">
         <div class="nm">${f.pre}<b>${f.suf}</b></div>
         <div class="role">${f.role}</div>
         <div class="tag">${f.tag}</div>
       </div>
     </div>`
  ).join('');

  // ---------- icons ----------
  const sizes=[128,96,64,40,28];
  $('#icons-row').innerHTML = sizes.map(s=>
    `<div class="ic">
       <div class="tile" style="width:${s}px;height:${s}px;">
         <svg viewBox="0 0 100 100">${rsMark('rig','var(--rig)')}</svg>
       </div>
       <div class="sz">${s}px</div>
     </div>`
  ).join('');

  // icon treatments: ink tile + accent mark, per tool
  const itTiles=[
    {k:'rig',col:'--rig'},{k:'change',col:'--change'},{k:'ship',col:'--ship'},{k:'claude',col:'--claude'}
  ];
  $('#icon-tiles').innerHTML = itTiles.map(t=>
    `<div class="it">
       <div class="box" style="background:var(--ink-2);border:1px solid var(--line);">
         <svg viewBox="0 0 100 100">${rsMark(t.k, `var(${t.col})`)}</svg>
       </div>
       <div class="lab">ink tile · accent</div>
     </div>`
  ).join('');

  // ---------- usage ----------
  $('#usage').innerHTML = `
    <div class="use ok">
      <div class="head"><span class="tick">✓</span> Do — full color (primary)</div>
      <div class="body" style="background:var(--ink);"><svg viewBox="0 0 100 100">${rsMark('rig','var(--rig)')}</svg></div>
      <div class="cap">Render the whole mark in one tool accent — brackets and glyph together.</div>
    </div>
    <div class="use ok">
      <div class="head"><span class="tick">✓</span> Do — single ink &amp; on light</div>
      <div class="body" style="background:var(--paper);"><svg viewBox="0 0 100 100">${rsMark('rig',P.ink)}</svg></div>
      <div class="cap">Where color can't go, drop to one ink — paper on dark, ink on light.</div>
    </div>
    <div class="use no">
      <div class="head"><span class="tick">✕</span> Don't — split into two colors</div>
      <div class="body" style="background:var(--ink);"><svg viewBox="0 0 100 100">${rsMark('rig','var(--ship)',{bracketInk:'var(--change)'})}</svg></div>
      <div class="cap">Brackets and glyph always share one color. Never two-tone them.</div>
    </div>
    <div class="use no">
      <div class="head"><span class="tick">✕</span> Don't — stretch or distort</div>
      <div class="body" style="background:var(--ink);"><svg viewBox="0 0 100 100" preserveAspectRatio="none" style="width:120px;height:60px;">${rsMark('rig',P.muted)}</svg></div>
      <div class="cap">Always scale uniformly. The grid is square.</div>
    </div>`;
})();
