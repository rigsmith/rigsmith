/* RigSmith docs theme — sidebar marks */
(function(){
  const set=(s,h)=>{const e=document.querySelector(s); if(e) e.innerHTML=h;};
  set('#side-mark', rsMark('rig','var(--rig)'));
  document.querySelectorAll('[data-mark]').forEach(el=>{
    el.innerHTML = rsMark(el.dataset.mark, `var(${el.dataset.col})`);
  });
})();
