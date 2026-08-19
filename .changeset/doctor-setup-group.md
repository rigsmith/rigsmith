---
type: feat
"github.com/rigsmith/rigsmith"
---

`rig doctor` now reports on rig itself: which binary is running, the family on `PATH`, and what rig has registered in your shell.

Doctor checked your *project's* environment — toolchains, per-project state, optional tools, `.rig.json` paths — and never checked rig's own. Two questions it couldn't answer:

- Where is rig, and is there more than one? The toolchain rows say "not on your PATH" when a tool is missing but never report *which* binary answered, and nothing looked at rig at all. A second copy earlier on `PATH` is invisible until it answers a command you meant for the one you just upgraded.
- What is actually wired into my shell? `rig setup` writes a marked block (the `rig()` wrapper that makes `rig cd` work, plus completions) and `rig alias install` writes another. rig wrote both and never read either back.

```
Setup
  ✓ rig        1.4.2 · /Users/john/.local/bin/rig
  ✓ family     shiprig, changerig, clauderig · /Users/john/.local/bin
  ! shell      not installed in ~/.zshrc — `rig cd` can't change your directory and
               tab completion is off; run `rig setup zsh`
  ✓ aliases    4 of 11 installed: rr, rb, rt, rcd · ~/.zshrc
```

What warns is chosen so the group stays worth reading:

- **Two copies on `PATH`** warns and names both, since the first is what runs. Running a binary that *isn't* on `PATH` doesn't warn — that's a source build or a `-dev` launcher, an ordinary dev loop — it just says what typing `rig` would run instead.
- **The setup block missing or stale** warns: `rig cd` silently doesn't change directories and completion silently doesn't complete, so neither failure announces itself. A startup file holding only a `--dev` block is called out by name — that is the state behind "I ran setup and `rig cd` still does nothing", because the block that got installed binds `rig-dev`.
- **The family and the aliases never warn.** Both are optional, and each companion's completion is loaded only when it's on `PATH`, so installing one later needs a new shell rather than a re-run. The rows exist to say what's there and where.
- **A shell rig writes no snippet for** (`ksh`, an unset `$SHELL`) is reported as a fact about the shell, and the aliases row says "not checked" rather than claiming none are installed off a file it never read.

The group runs before the ecosystem checks and, unlike them, runs everywhere: doctor used to return early with "no recognized projects found here — nothing to check", which skipped the whole report in exactly the directory you'd be standing in while asking why `rcd` does nothing. That directory now gets the Setup group and a note that only it ran.

Nothing here is offered to `rig doctor --fix`. Splicing a block into your startup file is `rig setup`'s job, it asks first, and `--fix` doesn't — so the rows point at the command instead of running it for you.

Also: **`rig alias list` marks the aliases that are actually installed.** It printed the fixed candidate set, identical on a machine with all eleven and one with none — so it answered "what could I install" while people were asking "why doesn't `rt` work". The mark comes from `installedAliases`, which already existed and was used in exactly one place: pre-checking boxes in the install checklist.

```
  ✓ rr   rig run  — Run the project
    rt   rig test  — Run the tests
  ✓ = installed in ~/.zshrc; `rig alias install` adds the rest
```
