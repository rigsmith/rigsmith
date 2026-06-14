# Release naming & monorepo consolidation

> **Status: PLAN — do not execute yet.** Land in-flight work first; this touches
> every module. Decisions locked 2026-06-14.

## Decisions

| Question | Decision |
|---|---|
| GitHub | One monorepo at `github.com/rigsmith/rigsmith` (org = `rigsmith`) |
| Go layout | **Single module** `github.com/rigsmith/rigsmith`, binaries under `cmd/` |
| npm | **Reserve names only** (`@rigsmith` scope + unscoped `rigsmith`); no working packages yet |
| Public install token | Always a **binary name** (`rig`, `clauderig`, `shiprig`, `changerig`) or **`rigsmith`** (= all). `cli`/`core` are internal names, never user-facing. |

## Target install matrix

| Channel | One tool | Everything |
|---|---|---|
| curl \| sh | `curl -fsSL https://rigsmith.sh \| sh` (`RIGSMITH_TOOLS=rig,shiprig` to subset) | default = all |
| Homebrew | `brew install rigsmith/tap/rig` (`/shiprig`, `/clauderig`, `/changerig`) | `brew install rigsmith/tap/rigsmith` (meta formula) |
| Go | `go install github.com/rigsmith/rigsmith/cmd/rig@latest` (`/cmd/shiprig`, …) | per-binary |
| npm (reserved) | `@rigsmith/rig`, `@rigsmith/clauderig`, `@rigsmith/shiprig`, `@rigsmith/changerig` | `rigsmith` (meta) → `npx rigsmith` |

`core` is a Go library only: importable at `github.com/rigsmith/rigsmith/core/...`. Never on brew/npm/curl.

## Current state (what we're collapsing)

Five modules, each with its own `go.mod`, tied together by `go.work` + `replace`:

- `cli/` → binary `rig`        · module `github.com/rigsmith/cli`
- `clauderig/` → `clauderig`    · `github.com/rigsmith/clauderig`
- `shiprig/` → `shiprig`        · `github.com/rigsmith/shiprig` (imports `changerig` + `core`)
- `changerig/` → `changerig`    · `github.com/rigsmith/changerig` (imports `core`)
- `core/` → library            · `github.com/rigsmith/core`
- `scripts/dev-install`, `scripts/source-install` → dev mains (import `core/gowork`)
- `tools/` → tool-directive-only module

Cross-module wiring to be deleted: every `replace … => ../…`, `go.work`, `go.work.sum`.

These flat module paths (`github.com/rigsmith/<tool>`) each imply a *separate repo* —
incompatible with one monorepo. That's the core reason for the refactor.

## Target layout

```
go.mod                       # module github.com/rigsmith/rigsmith  (the ONLY go.mod)
cmd/
  rig/main.go                # was cli/main.go
  clauderig/main.go
  shiprig/main.go
  changerig/main.go
core/…                       # PUBLIC library, import path .../core/…  (unchanged tree)
internal/
  rig/…                      # was cli/internal
  clauderig/…                # was clauderig/{internal,commands}
  shiprig/…                  # was shiprig/internal
  changerig/…                # was changerig/{parity,commands,cmdtest}
scripts/
  dev-install/main.go        # in-module main (no go.mod), run via `go run ./scripts/dev-install`
  source-install/main.go
```

Key simplification: **one module means `internal/` is shared freely across the
whole repo.** `shiprig` importing `changerig` logic becomes a normal
`internal/changerig/...` import — no module boundary, no `replace`. Only `core`
stays public (outside `internal/`) so external users can import it.

## Migration phases

### Phase 0 — Namespace land-grab — ✅ DONE (secured 2026-06-14)
- [x] GitHub org `rigsmith` — owned (Organization, created 2026-06-11). Locks every `github.com/rigsmith/*` path, including the future `homebrew-tap`.
- [x] npm — unscoped `rigsmith` published as `0.0.1` placeholder; `@rigsmith` scope reserved (org created). Decision: names only, no binary-wrapper packages yet.
- [x] Domains — `rigsmith.dev` + `rigsmith.sh` owned.
- [ ] Homebrew tap repo `rigsmith/homebrew-tap` — not yet created; deferred to Phase 3 (goreleaser can create it). Org ownership already prevents squatting.

### Phase 1 — Collapse to one module — ✅ DONE (branch `refactor/single-module-monorepo`)
1. New root `go.mod`: `module github.com/rigsmith/rigsmith`, `go 1.26`. Merge the union of all five `require` blocks; resolve versions; `go mod tidy`.
2. Move trees:
   - `cli/main.go` → `cmd/rig/main.go`; `cli/internal` → `internal/rig`.
   - `clauderig/main.go` → `cmd/clauderig/main.go`; `clauderig/{internal,commands}` → `internal/clauderig/…`.
   - `shiprig/main.go` → `cmd/shiprig/main.go`; `shiprig/internal` → `internal/shiprig`.
   - `changerig/main.go` → `cmd/changerig/main.go`; `changerig/{parity,commands,cmdtest}` → `internal/changerig/…`.
   - `core/` stays at repo root.
3. Rewrite imports: `github.com/rigsmith/core` → `github.com/rigsmith/rigsmith/core`; `github.com/rigsmith/{clauderig,shiprig,changerig}/X` → `github.com/rigsmith/rigsmith/internal/<tool>/X`. (Scripted find/replace, then `go build ./...`.)
4. Delete: all per-module `go.mod`/`go.sum`, all `replace` directives, `go.work`, `go.work.sum`, the `tools/` module (fold tool directives into root `go.mod` or a `tools.go`).
5. `go build ./...` && `go test ./...` green.

### Phase 2 — Fix tool discovery — ✅ DONE (in the same branch/PR as Phase 1)
- `core/gowork` repurposed from a `go.work` parser to a `cmd/` walker:
  `FindRoot` now finds `go.mod`, `Tools` scans `cmd/<tool>` for `// Command`
  mains. Both installers (`scripts/dev-install`, `scripts/source-install`)
  consume it unchanged and dropped their now-dead `scripts/` skip.
- `rig`'s verb-surfacing (`internal/rig/cli/scripts.go`) now scans `scripts/`
  on disk (so `rig dev-install`/`rig source-install` work without `go.work`),
  while still honoring `go.work` `use` entries for multi-module workspaces.
  `cmd/` is intentionally *not* auto-scanned, to keep the product binaries
  (`rig`/`shiprig`/…) out of rig's verb space.
- Verified end-to-end: `source-install` builds all 4 `cmd/` tools; `rig --help`
  surfaces the `scripts/` verbs; `dev-install` writes all 8 launchers.

### Phase 3 — Release plumbing — 🟡 PARTIAL (config done in this PR; tap/installer deferred)
- [x] `.goreleaser.yaml`: builds now use `main: ./cmd/<tool>` (dropped the
  per-dir `dir:`/`main: .`); rig ldflags path updated to
  `github.com/rigsmith/rigsmith/internal/rig/cli.version`. changerig build block
  still commented (release-scope decision — `cmd/changerig` already builds).
- [x] CI (`.github/workflows/ci.yml`): single `go test ./...` / `go vet ./...` /
  `gofmt -l .`; `go-version-file`/cache → root `go.mod`/`go.sum`.
- [ ] Enable the commented `brews:` block → tap `rigsmith/homebrew-tap`, one
  formula per binary + a `rigsmith` meta formula. (Deferred.)
- [ ] curl|sh installer: confirm it drops all binaries; wire `RIGSMITH_TOOLS`
  subset. (Deferred.)

### Phase 4 — Repo move & docs (last)
- Transfer/rename `JohnCampionJr/rigsmith` → `rigsmith/rigsmith` (GitHub keeps a
  redirect, but update remotes + module path references).
- Update README/docs install commands to the matrix above. Drop public mentions
  of `cli`/module names.
- Verify the `main-merge-clobber` discipline: confirm the transfer landed and
  origin/main is intact before closing out.

## Risks / watch-items
- **Import-rewrite blast radius:** every `github.com/rigsmith/*` import string changes. Do it scripted + `go build ./...` as the gate; expect a large diff.
- **`internal/` reachability:** fine within one module, but double-check no _test_ helper or external consumer relied on a now-`internal` package.
- **`go install @latest` needs a tag** on `rigsmith/rigsmith` post-move; pre-tag it can't resolve.
- **npm "no Node" story:** reserving names is free; building real wrapper packages later reintroduces a Node toolchain — revisit deliberately.
