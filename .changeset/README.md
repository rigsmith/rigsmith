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

`add` writes a `.changeset/*.md` in the shared @changesets format (the changed
package — `github.com/rigsmith/rigsmith` — its bump level, and a summary line
that becomes the changelog entry). `changerig status` shows the pending plan;
`changerig version` (run by the release pipeline) consumes the files.

See [the lifecycle docs](https://rigsmith.dev/changerig/lifecycle) for the full
workflow.
