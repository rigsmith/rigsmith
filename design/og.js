/* RigSmith social / repo assets — true-size OG cards, GitHub headers, favicons */
(function(){
  const $set=(sel,html)=>{const el=document.querySelector(sel); if(el) el.innerHTML=html;};
  const tools = {
    rig:    {pre:'',       suf:'rig',    col:'--rig',    tag:'Convention-first dev launcher', repo:'rigsmith/rig'},
    change: {pre:'change', suf:'Rig',    col:'--change', tag:'Changeset lifecycle, done lean', repo:'rigsmith/changerig'},
    ship:   {pre:'ship',   suf:'Rig',    col:'--ship',   tag:'The release front door', repo:'rigsmith/shiprig'},
    claude: {pre:'claude', suf:'Rig',    col:'--claude', tag:'Sync your Claude Code setup', repo:'rigsmith/clauderig'},
  };

  // ---- OG cards (1200x630) ----
  const ogMaster = `
    <div class="og card" data-w="1200" data-h="630" style="--accent:var(--rig);">
      <div class="og-grid"></div>
      <div class="og-glow"></div>
      <div class="og-in">
        <div class="og-eyebrow">rigsmith.dev</div>
        <div class="og-lockup">
          <svg viewBox="0 0 100 100">${rsMark('rig','var(--rig)')}</svg>
          <span class="wm"><b>Rig</b>Smith</span>
        </div>
        <div class="og-tag">One rig, four tools for the <b>whole dev loop</b>.</div>
        <div class="og-chips">
          ${Object.entries(tools).map(([k,t])=>`<span class="og-chip"><svg viewBox="0 0 100 100">${rsMark(k,`var(${t.col})`)}</svg><span class="lite">${t.pre}</span><b>${t.suf}</b></span>`).join('')}
        </div>
      </div>
    </div>`;

  const ogTool = (k,t) => `
    <div class="og card" data-w="1200" data-h="630" style="--accent:var(${t.col});">
      <div class="og-grid"></div>
      <div class="og-glow"></div>
      <div class="og-in tool">
        <svg class="og-bigmark" viewBox="0 0 100 100">${rsMark(k,`var(${t.col})`)}</svg>
        <div class="og-eyebrow" style="color:var(${t.col})">rigsmith.dev / ${t.pre}${t.suf}</div>
        <div class="og-lockup"><span class="wm big"><span class="lite">${t.pre}</span><b>${t.suf}</b></span></div>
        <div class="og-tag">${t.tag}.</div>
        <div class="og-install"><span class="d">$</span> ${t.pre?t.pre+t.suf.toLowerCase():'rig'} <span class="dim">--help</span></div>
      </div>
    </div>`;

  $set('#og-cards', ogMaster + Object.entries(tools).map(([k,t])=>ogTool(k,t)).join(''));

  // ---- GitHub repo headers (1280x320) ----
  const gh = (k,t) => `
    <div class="gh card" data-w="1280" data-h="320" style="--accent:var(${t.col});">
      <div class="gh-rule"></div>
      <div class="gh-in">
        <svg class="gh-mark" viewBox="0 0 100 100">${rsMark(k,`var(${t.col})`)}</svg>
        <div class="gh-text">
          <div class="wm gh-name"><span class="lite">${t.pre}</span><b>${t.suf}</b></div>
          <div class="gh-tag">${t.tag}</div>
          <div class="gh-repo">github.com/${t.repo}</div>
        </div>
      </div>
    </div>`;
  $set('#gh-cards', Object.entries(tools).map(([k,t])=>gh(k,t)).join(''));

  // ---- favicons ----
  const sizes=[16,32,48,64,128,180];
  $set('#fav-row', sizes.map(s=>`
    <div class="fav-item">
      <div class="fav-tile" style="width:${s}px;height:${s}px;">
        <svg viewBox="0 0 100 100">${rsMark('rig','var(--rig)')}</svg>
      </div>
      <div class="fav-sz">${s}</div>
    </div>`).join(''));
  // maskable / rounded app icon previews per tool
  $set('#fav-apps', Object.entries(tools).map(([k,t])=>`
    <div class="fav-app">
      <div class="fav-appicon" style="background:var(--ink);">
        <svg viewBox="0 0 100 100">${rsMark(k,`var(${t.col})`)}</svg>
      </div>
      <div class="fav-sz">${t.pre}${t.suf}</div>
    </div>`).join(''));

  // scale cards to fit their column
  function fit(){
    document.querySelectorAll('.card').forEach(c=>{
      const w=+c.dataset.w, h=+c.dataset.h;
      const avail=c.parentElement.clientWidth;
      const s=Math.min(1, avail/w);
      c.style.width=w+'px'; c.style.height=h+'px';
      c.style.transform=`scale(${s})`;
      c.style.transformOrigin='top left';
      c.parentElement.style.height=(h*s)+'px';
    });
  }
  window.addEventListener('resize', fit);
  fit();
})();
