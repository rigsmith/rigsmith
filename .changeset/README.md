# Changesets

This repo sources releases from **changeset files** (`versioning.source =
"changesets"` in `config.json`). Describe each change in a small markdown file
and `changerig version` turns the accumulated changesets into version bumps and
changelog entries.

```sh
changerig add -t feat -m "Add a feature"   # type-driven bump
changerig add --bump minor -m "…"          # explicit bump
changerig add                              # interactive: pick bump + message
```

Give each changeset a **type** (`feat`, `fix`, `refactor`, `build`, …) and, for
anything belonging to one tool, a **scope** (`rig`, `clauderig`, `changerig`,
`shiprig`). The type picks the changelog section and derives the bump; the scope
becomes the bullet's lead-in and groups that tool's entries together. `add`
infers the scope from the files your branch touched, so in practice you only
type it when it guesses wrong.

Leave the bump off a typed changeset — `"github.com/rigsmith/rigsmith"` with no
`: minor` — and the type decides it.

This repo has one releasable package, so `add` selects it for you. In a
workspace with several, a non-interactive `add` needs `-p <package>`: it will
not write a changeset that names none, because every later step ignores such a
file.

`add` writes a `.changeset/*.md` in the shared @changesets format (the changed
package — `github.com/rigsmith/rigsmith` — its bump level, and a summary line
that becomes the changelog entry). `changerig status` shows the pending plan;
`changerig version` (run by the release pipeline) consumes the files.

See [the lifecycle docs](https://rigsmith.dev/changerig/lifecycle) for the full
workflow.
