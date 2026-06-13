/* RigSmith index hub */
(function(){
  const set=(s,h)=>{const e=document.querySelector(s); if(e) e.innerHTML=h;};
  set('#hub-mark', rsMark('rig','var(--rig)'));
  const items=[
    {href:'RigSmith Brand.html',       k:'rig',    col:'--rig',    n:'Brand & Logo System', d:'Marks, construction, color, family, lockups and usage rules.', tag:'foundation'},
    {href:'RigSmith Landing.html',     k:'change', col:'--change', n:'RigSmith.dev Landing', d:'Marketing site — hero, the four-tool family, install.', tag:'web'},
    {href:'RigSmith CLI.html',         k:'ship',   col:'--ship',   n:'CLI Aesthetic',        d:'Banner, color system, log states and per-tool terminal voice.', tag:'cli'},
    {href:'RigSmith Social Assets.html',k:'claude',col:'--claude', n:'Social & Repo Assets', d:'Open Graph cards, GitHub headers and favicons.', tag:'assets'},
    {href:'RigSmith Stickers.html',    k:'rig',    col:'--rig',    n:'Sticker Sheet',        d:'Die-cut marks, wordmark pills and a hero badge.', tag:'swag'},
    {href:'RigSmith Animated Logo.html',k:'change',col:'--change', n:'Animated Logo',        d:'Assembly animation plus a signature idle loop per tool.', tag:'motion'},
    {href:'RigSmith Docs Theme.html',  k:'ship',   col:'--ship',   n:'Docs Theme',           d:'Documentation layout — sidebar, code blocks, callouts.', tag:'docs'},
  ];
  set('#hub-grid', items.map(it=>`
    <a class="hub-card" href="${encodeURI(it.href)}" style="--accent:var(${it.col});">
      <div class="hub-glow"></div>
      <svg class="hub-icon" viewBox="0 0 100 100">${rsMark(it.k,`var(${it.col})`)}</svg>
      <div class="hub-body">
        <div class="hub-tag">${it.tag}</div>
        <div class="hub-name">${it.n}</div>
        <div class="hub-desc">${it.d}</div>
      </div>
      <div class="hub-arrow">→</div>
    </a>`).join(''));
})();
