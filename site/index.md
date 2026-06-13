---
layout: home

hero:
  name: RigSmith
  text: One rig, four tools for the whole dev loop.
  tagline: >-
    A family of single-binary CLIs that wrap your stack — from the inner
    build/test loop to changesets, releases, and your Claude Code setup.
    Change It, Ship It.
  image:
    src: /marks/rigsmith.svg
    alt: RigSmith
  actions:
    - theme: brand
      text: Get started
      link: /guide/installation
    - theme: alt
      text: View on GitHub
      link: https://github.com/JohnCampionJr/rigsmith

features:
  - title: Zero runtime
    details: >-
      Every tool is a single, statically-linked Go binary. No .NET runtime, no
      Node — install with curl | sh, Homebrew, or Scoop on any machine.
  - title: Convention-first
    details: >-
      The same verb works across .NET, Node, Go, and Rust. rig detects your
      stack and runs the right native command — zero config to start.
  - title: One shared engine
    details: >-
      changeRig and shipRig run the same release planner: semver, dependency
      cascade, linked/fixed grouping. Standard library only, zero dependencies.
---

<div class="rs-tools">
  <a class="rs-tool acc-rig" href="/rig/">
    <div class="glow"></div>
    <div class="rs-tool-head">
      <img src="/marks/rig.svg" alt="rig" />
      <div>
        <div class="rs-tool-name wm"><b>rig</b></div>
        <div class="rs-tool-tag">dev launcher</div>
      </div>
    </div>
    <p class="rs-tool-desc">Convention-first dev launcher. Auto-detects your stack — .NET, Node, Go, Rust — and runs the right native command for build, test, run, and format.</p>
    <div class="rs-verbs"><code>build</code><code>test</code><code>run</code><code>fmt</code><code>coverage</code><code>doctor</code><code>kill</code><code>cd</code></div>
  </a>

  <a class="rs-tool acc-change" href="/changerig/">
    <div class="glow"></div>
    <div class="rs-tool-head">
      <img src="/marks/changeRig.svg" alt="changeRig" />
      <div>
        <div class="rs-tool-name wm"><span class="lite">change</span><b>Rig</b></div>
        <div class="rs-tool-tag">changeset lifecycle</div>
      </div>
    </div>
    <p class="rs-tool-desc">Lean changeset lifecycle: init → add → status → version. Bumps versions and writes the CHANGELOG with a full dependency cascade. Aliased changeset.</p>
    <div class="rs-verbs"><code>init</code><code>add</code><code>status</code><code>version</code></div>
  </a>

  <a class="rs-tool acc-ship" href="/shiprig/">
    <div class="glow"></div>
    <div class="rs-tool-head">
      <img src="/marks/shipRig.svg" alt="shipRig" />
      <div>
        <div class="rs-tool-name wm"><span class="lite">ship</span><b>Rig</b></div>
        <div class="rs-tool-tag">release front door</div>
      </div>
    </div>
    <p class="rs-tool-desc">The release front door. Everything changeRig does, plus publish, tag, and pre-release orchestration through a configurable release-step pipeline.</p>
    <div class="rs-verbs"><code>version</code><code>publish</code><code>tag</code><code>pre</code><code>release</code></div>
  </a>

  <a class="rs-tool acc-claude" href="/clauderig/">
    <div class="glow"></div>
    <div class="rs-tool-head">
      <img src="/marks/claudeRig.svg" alt="claudeRig" />
      <div>
        <div class="rs-tool-name wm"><span class="lite">claude</span><b>Rig</b></div>
        <div class="rs-tool-tag">setup sync</div>
      </div>
    </div>
    <p class="rs-tool-desc">Syncs your Claude Code setup — config, skills, session history — across machines via a private git repo, with cross-OS path correction and secret stripping.</p>
    <div class="rs-verbs"><code>init</code><code>sync</code><code>pull</code><code>restore</code><code>status</code></div>
  </a>
</div>
