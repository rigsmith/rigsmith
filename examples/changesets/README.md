# changesets test fixture

A self-contained `changerig` workspace — a small JS monorepo with three packages
and a spread of **pending changesets** — for exercising the changeset lifecycle,
most usefully the **`changerig browse`** browser/manager:

```sh
cd examples/changesets
changerig browse        # interactive: ↑/↓, enter to view, d delete, e edit, q quit
changerig browse | cat  # non-TTY: a plain one-line-per-changeset list
changerig status        # the release plan these changesets produce
```

## Layout

- **`package.json`** — the workspace container (`"workspaces": ["packages/*"]`,
  `private`), so discovery returns the three packages, not the root.
- **`packages/core`** (`@acme/core` 1.4.2), **`packages/cli`** (`@acme/cli`
  0.8.0, depends on `@acme/core`), **`packages/utils`** (`@acme/utils` 2.1.3).
- **`.changeset/config.json`** — marks this dir as the changeset root, so
  `changerig` here resolves to the fixture, not the parent repo.

## The changesets (deliberately varied, to show every browser state)

| File | Badge | Why it's here |
|---|---|---|
| `spotty-pandas-cheer` | `minor` | explicit bump, single package, conventional summary |
| `fluffy-rivers-mend` | `fix` | `type: fix` (patch), single package |
| `brave-suns-shift` | `feat!` | breaking, **multi-package** major, multi-line body |
| `quiet-lions-derive` | `feat` | **no explicit bump** — derived from `type` |
| `long-winded-wizard` | `minor` | long summary → scroll the detail viewport |
| `empty-no-release` | `chore` | **empty changeset** (no releases) → "none" in detail |

Nothing here needs installing or building; the manifests alone drive discovery.
Delete a changeset in the browser (`d`) and re-`git checkout` the file to reset.
