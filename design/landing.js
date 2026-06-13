/* RigSmith.dev landing — renders marks + tool cards from the shared mark system */
(function(){
  const $ = s => document.querySelector(s);
  const setSvg = (sel, inner) => { const el=$(sel); if(el) el.innerHTML = inner; };

  // nav + hero + footer marks
  setSvg('#nav-mark',   rsMark('rig','var(--rig)'));
  setSvg('#hero-mark',  rsMark('rig','var(--rig)'));
  setSvg('#foot-mark',  rsMark('rig','var(--muted)'));

  // ---------- tools ----------
  const tools = [
    {
      k:'rig', pre:'', suf:'rig', col:'--rig',
      tag:'dev launcher',
      desc:'Convention-first dev launcher. Auto-detects your stack — .NET, Node, Go, Rust — and runs the right native command for build, test, run and format.',
      verbs:['build','test','run','fmt','coverage','doctor','kill','cd'],
      cmd:[['$ ','rig test'],['','rig · detected Go (go.mod)'],['','→ go test ./...'],['ok','  rigsmith/cli   0.412s']],
    },
    {
      k:'change', pre:'change', suf:'Rig', col:'--change',
      tag:'changeset lifecycle',
      desc:'Lean changeset lifecycle: init → add → status → version. Bumps versions and writes the CHANGELOG with a full dependency cascade. Aliased changeset.',
      verbs:['init','add','status','version'],
      cmd:[['$ ','changeRig add'],['?','  which packages changed?'],['◆','  cli, release  ·  minor'],['✓','  .changeset/funny-foxes.md']],
    },
    {
      k:'ship', pre:'ship', suf:'Rig', col:'--ship',
      tag:'release front door',
      desc:'The release front door. Everything changeRig does, plus publish, tag and pre-release orchestration through a configurable release-step pipeline.',
      verbs:['version','publish','tag','pre','pipeline'],
      cmd:[['$ ','shipRig publish'],['→','  build → tag → publish'],['✓','  v1.4.0 tagged'],['✓','  pushed 3 binaries']],
    },
    {
      k:'claude', pre:'claude', suf:'Rig', col:'--claude',
      tag:'setup sync',
      desc:'Syncs your Claude Code setup — config, skills, session history — across machines via a private git repo, with cross-OS path correction and secret stripping.',
      verbs:['init','sync','pull','push','status'],
      cmd:[['$ ','claudeRig sync'],['→','  strip secrets · fix paths'],['✓','  3 skills, 12 sessions'],['✓','  pushed → origin/main']],
    },
  ];

  $('#tools-grid').innerHTML = tools.map(t => `
    <article class="tool" style="--accent:var(${t.col});">
      <div class="tool-glow"></div>
      <header class="tool-head">
        <svg class="tool-mark" viewBox="0 0 100 100">${rsMark(t.k, `var(${t.col})`)}</svg>
        <div class="tool-id">
          <div class="tool-name wm"><span class="lite">${t.pre}</span><b>${t.suf}</b></div>
          <div class="tool-tag">${t.tag}</div>
        </div>
      </header>
      <p class="tool-desc">${t.desc}</p>
      <div class="tool-verbs">${t.verbs.map(v=>`<code class="verb">${v}</code>`).join('')}</div>
      <div class="term">
        ${t.cmd.map(([p,l])=>{
          const cls = p==='$ '?'p-prompt':p==='✓'?'p-ok':p==='→'?'p-arrow':p==='?'||p==='◆'?'p-q':'p-dim';
          const pre = p==='$ '?'<span class="dollar">$</span> ':p?`<span class="${cls}">${p}</span> `:'';
          return `<div class="term-line ${cls}">${pre}<span>${l.replace(/^\s+/,m=>'&nbsp;'.repeat(m.length))}</span></div>`;
        }).join('')}
      </div>
    </article>
  `).join('');
})();
