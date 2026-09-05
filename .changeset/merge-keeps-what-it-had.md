---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Three ways a conflicted sync could quietly lose data, all fixed.

A conflicted manifest was round-tripped through a struct that did not model `links`, so restore lost its record of which worktree project directories share a `memory/` directory with their main project. Links are merged from both machines now, and a test fails if the manifest ever grows another field the merge does not carry — the trap was never specific to links.

A conflicted transcript had the local side deduplicated against itself before the remote's tail was appended. Two records that serialise identically are rare but not impossible, and the merge is not the place to decide someone's conversation contained one fewer turn than it does. The local file is carried over exactly as it was.

The device and manifest mergers matched on filename alone, so a synced project of your own containing a file called `clauderig-devices.json` would be run through a merger that understands a different format entirely and rewritten as one. They match at the repo root now.

Also: `clauderig repo prune` preserves merge commits inside the retained window rather than flattening them into a straight line, and moves the branch only if it is still where the rebuild started — so a sync that commits while history is being rebuilt is not discarded.
