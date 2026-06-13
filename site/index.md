---
layout: home

hero:
  name: rigsmith
  text: One toolbelt for polyglot monorepos.
  tagline: >-
    A family of convention-first, zero-runtime-dependency CLIs — and the shared
    Go engine behind them. The same verb works in .NET, Node, Go, and Rust.
  actions:
    - theme: brand
      text: Get started
      link: /guide/installation
    - theme: alt
      text: View on GitHub
      link: https://github.com/JohnCampionJr/rigsmith

features:
  - title: rig
    details: >-
      The convention-first dev launcher. rig build / test / run / format detects
      the repo and runs the right native command — zero config to start.
    link: /rig/
    linkText: rig docs
  - title: changerig
    details: >-
      The lean changeset tool: init → add → status → version, with a dependency
      cascade and linked/fixed grouping. Aliased changeset.
    link: /changerig/
    linkText: changerig docs
  - title: relrig
    details: >-
      The release front door. Everything changerig does, plus tag / publish and
      a configurable release pipeline driven by .changeset/release.jsonc.
    link: /relrig/
    linkText: relrig docs
  - title: clauderig
    details: >-
      Sync your Claude Code setup across machines via a private git repo — with
      cross-OS path correction and secrets that never leave the machine.
    link: /clauderig/
    linkText: clauderig docs
  - title: core
    details: >-
      The shared engine: semver, changeset parsing, the release planner, and the
      plugin contract. Standard library only — zero external dependencies.
    link: /core/
    linkText: core docs
  - title: Zero runtime
    details: >-
      Every tool is a single, statically-linked Go binary. No .NET runtime, no
      Node — install with curl | sh, Homebrew, or Scoop on any machine.
    link: /guide/installation
    linkText: Install
---
