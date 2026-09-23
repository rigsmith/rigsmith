# Changesets

This repo sources releases from **changeset files** (`versioning.source =
"changesets"` in `config.json`). Describe each change in a small markdown file
and `changerig version` turns the accumulated changesets into version bumps and
changelog entries.

```sh
changerig add -p github.com/rigsmith/rigsmith -t feat -m "Add a feature"   # type-driven bump
changerig add -p github.com/rigsmith/rigsmith --bump minor -m "…"          # explicit bump
changerig add                                                              # interactive: pick package, bump + message
```

Give each changeset a **type** (`feat`, `fix`, `refactor`, `build`, …) and, for
anything belonging to one tool, a **scope** (`rig`, `clauderig`, `changerig`,
`shiprig`). The type picks the changelog section and derives the bump; the scope
becomes the bullet's lead-in and groups that tool's entries together. `add`
infers the scope from the files your branch touched, so in practice you only
type it when it guesses wrong.

Leave the bump off a typed changeset — `"github.com/rigsmith/rigsmith"` with no
`: minor` — and the type decides it.

## Two packages, two release lines

This repo has two releasable packages, and a non-interactive `add` needs
`-p <package>` to pick one: it will not write a changeset that names none,
because every later step ignores such a file.

| Package | Version lives in | Tag | Released by |
| --- | --- | --- | --- |
| `github.com/rigsmith/rigsmith` — the CLIs | root `go.mod`'s `// rigsmith:version` comment | `vX.Y.Z` | [`goreleaser.yml`](../.github/workflows/goreleaser.yml) |
| `github.com/rigsmith/rigsmith/ui` — the claudeRig UI | `ui/go.mod`'s `// rigsmith:version` comment | `ui/vX.Y.Z` | [`release-ui.yml`](../.github/workflows/release-ui.yml) |

### Recording a UI change

Name the UI module and give it the `clauderig-ui` scope — `add` does not infer
that scope from files under `ui/`, so pass it:

```sh
changerig add -p github.com/rigsmith/rigsmith/ui -t fix --scope clauderig-ui -m "Fix the tray's profile list"
```

That writes:

```md
---
type: fix
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

Fix the tray's profile list
```

`changerig status --verbose` then shows only the UI moving
(`patch  github.com/rigsmith/rigsmith/ui  0.4.0 → 0.4.1`). When the shiprig[bot]
"chore: release" PR carrying it is merged, `changerig version` has already
bumped `ui/go.mod` and written `ui/CHANGELOG.md`, and
[`release.yml`](../.github/workflows/release.yml)'s `shiprig tag` pushes
`ui/vX.Y.Z`, which starts `release-ui.yml`. A UI-only release makes no `vX.Y.Z`
tag, so GoReleaser doesn't run; a change that touches both needs both packages
(repeat `-p`, or one changeset each so each changelog gets its own wording).

## What `add` writes

`add` writes a `.changeset/*.md` in the shared @changesets format (the changed
package, its bump level or type, and a summary line that becomes the changelog
entry). `changerig status` shows the pending plan; `changerig version` (run by
the release pipeline) consumes the files.

See [the lifecycle docs](https://rigsmith.dev/changerig/lifecycle) for the full
workflow.
