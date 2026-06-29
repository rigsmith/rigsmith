---
"github.com/rigsmith/rigsmith": patch
---

`rig`'s dev-verb discovery no longer double-counts a project that has a Velopack (or Electron/Tauri) overlay file beside it. Overlay ecosystems re-emit their base-language project for the release path; surfacing them as dev targets produced a duplicate that, because `topoSort` keys by name, shadowed the real base target with an overlay copy that maps no `run`/`build`/`test` verb. The visible symptom: a configured `defaultProject` naming such an app "didn't match a runnable project", so a bare `rig run` opened the picker instead of launching it. Dev verbs now act only on the base ecosystem.
