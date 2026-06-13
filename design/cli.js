/* RigSmith CLI aesthetic — renders marks + ANSI-style palette swatches */
(function(){
  const $ = s => document.querySelector(s);
  if($('#cli-mark')) $('#cli-mark').innerHTML = rsMark('rig','var(--rig)');
  document.querySelectorAll('[data-mark]').forEach(el=>{
    el.innerHTML = rsMark(el.dataset.mark, `var(${el.dataset.col||'--rig'})`);
  });
})();
