// Package claudemd installs clauderig's managed instructions into a CLAUDE.md as
// marker-delimited blocks, the de-facto convention tools use to own a region of a
// shared instruction file. Everything between a block's BEGIN and END markers is
// clauderig's to rewrite; everything outside is the user's and is never touched.
// Re-installing replaces a block in place (idempotent), so a machine that pulls a
// newer clauderig gets the latest guidance on next install.
//
// clauderig owns more than one concern, so the file is modelled as a set of
// independent Sections — one for the worktree/PR discipline the guard enforces,
// one for how to use the rigsmith CLI family — each fenced by its own markers and
// managed separately. The slug after the colon in each marker keeps them from
// colliding.
package claudemd

import (
	"os"
	"strings"
)

// A Section is one independently-managed block: a unique pair of BEGIN/END
// markers and the instruction body Claude reads between them. Outside the markers
// the file is the user's; a Section only ever rewrites its own region.
type Section struct {
	Begin string
	End   string
	body  string
}

// Action describes what a Section's Install/Uninstall did.
type Action string

const (
	Installed Action = "installed"
	Updated   Action = "updated"
	Unchanged Action = "unchanged"
	Removed   Action = "removed"
	Absent    Action = "absent"
)

// Worktree is the worktree/PR-discipline block: it explains the rules the
// `clauderig guard` hook enforces, so a session works *with* the guard instead of
// bumping into denials.
var Worktree = Section{
	Begin: Begin,
	End:   End,
	body: `<!-- Managed by clauderig. Run ` + "`clauderig guide`" + ` to update; edits inside this block are overwritten. -->
## Worktree & PR discipline (enforced by ` + "`clauderig guard`" + `)

A PreToolUse hook guards this environment. Work *with* it:

- **Never use the EnterWorktree/ExitWorktree tools, and never ` + "`cd`" + ` out of the
  repo root in a Bash command.** Both move this session's working directory, and
  Claude Code keys chat history to that folder path — moving it scrambles the
  conversation. They are denied. To act elsewhere, use an absolute path,
  ` + "`git -C <dir> …`" + `, or a subshell ` + "`(cd <dir> && …)`" + ` (which doesn't move this shell).
- **Don't write code on ` + "`main`/`master`" + `.** Make a branch + worktree first:
  run ` + "`clauderig worktree new <branch>`" + `. It creates a sibling checkout at
  ` + "`<repo>-worktrees/<branch>`" + ` and opens it in a *new* VS Code window for review —
  this window stays put. Edit files in the worktree by absolute path, run git via
  ` + "`git -C <worktree> …`" + `, then push and open a PR.
- **Docs and root config may go on the base branch directly** — ` + "`*.md`" + `, the
  ` + "`docs/`" + ` and ` + "`.github/`" + ` trees, and top-level config (` + "`*.toml`, `*.yml`, `*.json`" + `,
  ` + "`LICENSE`, `.gitignore`" + `). Everything else counts as code and needs a PR.
- **Override**, only when you must change code on the base branch:
  ` + "`export CLAUDERIG_ALLOW_MAIN=1`" + ` (this session) or ` + "`touch .claude/allow-main`" + ` (this repo).

Keep one VS Code window pinned to the primary repo as the continuous chat; treat
worktree windows as review/diff only.`,
}

// RigTools documents the rigsmith CLI family (rig / changerig / shiprig) so a
// session reaches for the convention-first launchers instead of raw per-ecosystem
// commands. Each tool gets its own subsection.
var RigTools = Section{
	Begin: "<!-- BEGIN clauderig:rig-tools -->",
	End:   "<!-- END clauderig:rig-tools -->",
	body: `<!-- Managed by clauderig. Run ` + "`clauderig guide`" + ` to update; edits inside this block are overwritten. -->
## rigsmith tools (rig / changerig / shiprig)

This machine has the rigsmith CLI family — convention-first launchers that run the
right native command per ecosystem (.NET / Node / Go / Rust). Prefer them over raw
` + "`go`/`dotnet`/`npm`" + ` invocations: one verb works everywhere, and they share the
same project detection.

### rig — the dev launcher

Drives the inner loop; the same verb resolves to each ecosystem's native command:

- ` + "`rig build` / `rig test` / `rig run` / `rig format` / `rig lint` / `rig typecheck`" + `.
- ` + "`rig info`" + ` — what rig discovered (root, ecosystem, dev commands, packages).
  Run it first when you're unsure how a repo builds.
- ` + "`rig coverage --min 80`" + ` — tests behind a coverage gate; ` + "`rig watch <verb>`" + ` re-runs on change.
- ` + "`-n`/`--dry-run`" + ` prints the command without running it; ` + "`-q`/`--quiet`" + ` hides the ` + "`→`" + ` echo.

No config is required — an optional ` + "`.rig.json`" + ` (JSONC) at the repo root supplies
only what can't be inferred.

### changerig — changesets

The changeset lifecycle. When a change is user-facing, record it in the same PR:

- ` + "`changerig add -p <pkg> --bump <patch|minor|major> -m \"<summary>\"`" + ` — write a changeset (interactive with no flags).
- ` + "`changerig status --verbose`" + ` — show the pending release plan.
- ` + "`changerig version`" + ` — bump versions and write ` + "`CHANGELOG.md`" + `, cascading bumps to dependents.

Don't hand-edit version numbers or changelogs — let ` + "`version`" + ` own them.

### shiprig — releases

The release front door: everything changerig does, plus publish/tag/pre
orchestration. Releasing is usually CI's job; locally:

- ` + "`shiprig status` / `shiprig version`" + ` — preview and apply the release plan.
- ` + "`shiprig publish`" + ` — registries + tags, idempotent and confirm-gated on a TTY (` + "`--yes`" + ` for CI).
- ` + "`shiprig release`" + ` — the configurable step pipeline (` + "`.changeset/release.jsonc`" + `).

Don't run ` + "`publish`/`release`" + ` unless explicitly asked — they push tags and hit registries.`,
}

// Sections is the full set clauderig manages, in the order they're written to a
// fresh file. Install/Uninstall/Status iterate this so adding a future block is a
// one-line change.
var Sections = []Section{Worktree, RigTools}

// Begin and End fence the worktree-discipline block. They remain top-level
// identifiers (rather than fields only) because that block predates the
// multi-section model and external callers/tests reference them by name.
const (
	Begin = "<!-- BEGIN clauderig:worktree-discipline -->"
	End   = "<!-- END clauderig:worktree-discipline -->"
)

// block is the section's full managed region without a trailing newline.
func (s Section) block() string { return s.Begin + "\n" + s.body + "\n" + s.End }

// Block returns the section's managed block as it is written to a file
// (newline-terminated).
func (s Section) Block() string { return s.block() + "\n" }

// Install writes the section's block into the CLAUDE.md at path, creating the
// file if absent. An existing block is replaced in place; otherwise the block is
// appended after a blank-line separator. Content outside the markers is preserved.
func (s Section) Install(path string) (Action, error) {
	cur, err := readOrEmpty(path)
	if err != nil {
		return "", err
	}
	blk := s.block()
	if start, end, ok := s.locate(cur); ok {
		next := cur[:start] + blk + cur[end:]
		if next == cur {
			return Unchanged, nil
		}
		return Updated, write(path, next)
	}
	var b strings.Builder
	b.WriteString(cur)
	switch {
	case cur == "":
		// fresh file, no separator needed
	case strings.HasSuffix(cur, "\n\n"):
	case strings.HasSuffix(cur, "\n"):
		b.WriteString("\n")
	default:
		b.WriteString("\n\n")
	}
	b.WriteString(s.Block())
	return Installed, write(path, b.String())
}

// Uninstall removes the section's block (and a blank line it left behind),
// leaving the rest of the file intact.
func (s Section) Uninstall(path string) (Action, error) {
	cur, err := readOrEmpty(path)
	if err != nil {
		return "", err
	}
	start, end, ok := s.locate(cur)
	if !ok {
		return Absent, nil
	}
	// Swallow a single trailing newline so removal doesn't leave a blank gap.
	if end < len(cur) && cur[end] == '\n' {
		end++
	}
	next := strings.TrimRight(cur[:start], "\n")
	if rest := cur[end:]; rest != "" {
		if next != "" {
			next += "\n\n"
		}
		next += strings.TrimLeft(rest, "\n")
	} else if next != "" {
		next += "\n"
	}
	return Removed, write(path, next)
}

// Present reports whether path carries the section's block.
func (s Section) Present(path string) (bool, error) {
	cur, err := readOrEmpty(path)
	if err != nil {
		return false, err
	}
	_, _, ok := s.locate(cur)
	return ok, nil
}

// locate returns the byte range [start,end) spanning the section's managed block
// (markers included, trailing newline excluded), and whether it was found.
func (s Section) locate(str string) (start, end int, ok bool) {
	i := strings.Index(str, s.Begin)
	if i < 0 {
		return 0, 0, false
	}
	j := strings.Index(str[i:], s.End)
	if j < 0 {
		return 0, 0, false
	}
	return i, i + j + len(s.End), true
}

// Block returns the worktree-discipline block as written to a file
// (newline-terminated). Retained as a package-level helper for callers that
// predate the multi-section model.
func Block() string { return Worktree.Block() }

// InstallAll installs every managed Section into path and rolls the per-section
// actions into one: Installed if any section was newly added, else Updated if any
// changed, else Unchanged. It suits callers (scope/doctor) that treat the guide
// as a single unit.
func InstallAll(path string) (Action, error) {
	roll := Unchanged
	for _, sec := range Sections {
		act, err := sec.Install(path)
		if err != nil {
			return "", err
		}
		roll = mergeAction(roll, act)
	}
	return roll, nil
}

// UninstallAll removes every managed Section from path, rolling the result into
// Removed if any section was present, else Absent.
func UninstallAll(path string) (Action, error) {
	roll := Absent
	for _, sec := range Sections {
		act, err := sec.Uninstall(path)
		if err != nil {
			return "", err
		}
		if act == Removed {
			roll = Removed
		}
	}
	return roll, nil
}

// AllPresent reports whether path carries every managed Section. It's false when
// any is missing, so a caller (doctor) re-runs Install to add the newcomers.
func AllPresent(path string) (bool, error) {
	for _, sec := range Sections {
		ok, err := sec.Present(path)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// mergeAction folds a section's action into the running roll-up: a fresh install
// dominates an update, which dominates "unchanged".
func mergeAction(roll, act Action) Action {
	switch {
	case roll == Installed || act == Installed:
		return Installed
	case roll == Updated || act == Updated:
		return Updated
	default:
		return Unchanged
	}
}

// Install writes the worktree-discipline block into the CLAUDE.md at path. It
// delegates to Worktree.Install; see that method for semantics.
func Install(path string) (Action, error) { return Worktree.Install(path) }

// Uninstall removes the worktree-discipline block from the CLAUDE.md at path.
func Uninstall(path string) (Action, error) { return Worktree.Uninstall(path) }

// Present reports whether path carries the worktree-discipline block.
func Present(path string) (bool, error) { return Worktree.Present(path) }

func readOrEmpty(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}

func write(path, content string) error {
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
