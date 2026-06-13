/* RigSmith sticker sheet */
(function(){
  const $set=(s,h)=>{const e=document.querySelector(s); if(e) e.innerHTML=h;};
  const tools=[
    {k:'rig',pre:'',suf:'rig',col:'--rig'},
    {k:'change',pre:'change',suf:'Rig',col:'--change'},
    {k:'ship',pre:'ship',suf:'Rig',col:'--ship'},
    {k:'claude',pre:'claude',suf:'Rig',col:'--claude'},
  ];

  // round die-cut mark stickers
  $set('#st-rounds', tools.map(t=>`
    <div class="sticker round" style="--accent:var(${t.col});">
      <div class="peel"></div>
      <svg viewBox="0 0 100 100">${rsMark(t.k,`var(${t.col})`)}</svg>
    </div>`).join(''));

  // wordmark pill stickers
  $set('#st-pills', tools.map(t=>`
    <div class="sticker pill" style="--accent:var(${t.col});">
      <svg viewBox="0 0 100 100">${rsMark(t.k,`var(${t.col})`)}</svg>
      <span class="wm"><span class="lite">${t.pre}</span><b>${t.suf}</b></span>
    </div>`).join(''));

  // hero badge + bracket + fun
  $set('#st-hero', `
    <div class="sticker hex" style="--accent:var(--rig);">
      <svg class="m" viewBox="0 0 100 100">${rsMark('rig','var(--rig)')}</svg>
      <span class="wm"><b>Rig</b>Smith</span>
    </div>`);
  $set('#st-fun', `
    <div class="sticker bracket"><span>[</span><b>•</b><span>]</span></div>
    <div class="sticker tag" style="--accent:var(--ship);"><span class="ok">✓</span> rig doctor</div>
    <div class="sticker tag" style="--accent:var(--rig);"><span class="d">$</span> rig ship it</div>
    <div class="sticker tag" style="--accent:var(--change);">one rig, four tools</div>`);
})();
