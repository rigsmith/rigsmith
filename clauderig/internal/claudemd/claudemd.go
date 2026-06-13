// Package claudemd installs clauderig's worktree-discipline instructions into a
// CLAUDE.md as a marker-delimited managed block, the de-facto convention tools
// use to own a region of a shared instruction file. Everything between the BEGIN
// and END markers is clauderig's to rewrite; everything outside is the user's and
// is never touched. Re-installing replaces the block in place (idempotent), so a
// machine that pulls a newer clauderig gets the latest guidance on next install.
package claudemd

import (
	"os"
	"strings"
)

// Begin and End fence the managed block. The slug after the colon lets a future
// clauderig own additional, independently-managed blocks without collision.
const (
	Begin = "<!-- BEGIN clauderig:worktree-discipline -->"
	End   = "<!-- END clauderig:worktree-discipline -->"
)

// body is the instruction text Claude reads. It explains the rules the
// `clauderig guard` hook enforces, so a session works *with* the guard instead of
// bumping into denials.
const body = `<!-- Managed by clauderig. Run ` + "`clauderig guide`" + ` to update; edits inside this block are overwritten. -->
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
worktree windows as review/diff only.`

// block is the full managed region without a trailing newline.
const block = Begin + "\n" + body + "\n" + End

// Block returns the managed block as it is written to a file (newline-terminated).
func Block() string { return block + "\n" }

// Action describes what Install/Uninstall did.
type Action string

const (
	Installed Action = "installed"
	Updated   Action = "updated"
	Unchanged Action = "unchanged"
	Removed   Action = "removed"
	Absent    Action = "absent"
)

// Install writes clauderig's block into the CLAUDE.md at path, creating the file
// if absent. An existing block is replaced in place; otherwise the block is
// appended after a blank-line separator. Content outside the markers is preserved.
func Install(path string) (Action, error) {
	cur, err := readOrEmpty(path)
	if err != nil {
		return "", err
	}
	if s, e, ok := locate(cur); ok {
		next := cur[:s] + block + cur[e:]
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
	b.WriteString(Block())
	return Installed, write(path, b.String())
}

// Uninstall removes clauderig's block (and a blank line it left behind), leaving
// the rest of the file intact.
func Uninstall(path string) (Action, error) {
	cur, err := readOrEmpty(path)
	if err != nil {
		return "", err
	}
	s, e, ok := locate(cur)
	if !ok {
		return Absent, nil
	}
	// Swallow a single trailing newline so removal doesn't leave a blank gap.
	if e < len(cur) && cur[e] == '\n' {
		e++
	}
	next := strings.TrimRight(cur[:s], "\n")
	if rest := cur[e:]; rest != "" {
		if next != "" {
			next += "\n\n"
		}
		next += strings.TrimLeft(rest, "\n")
	} else if next != "" {
		next += "\n"
	}
	return Removed, write(path, next)
}

// Present reports whether path carries clauderig's block.
func Present(path string) (bool, error) {
	cur, err := readOrEmpty(path)
	if err != nil {
		return false, err
	}
	_, _, ok := locate(cur)
	return ok, nil
}

// locate returns the byte range [start,end) spanning the managed block (markers
// included, trailing newline excluded), and whether it was found.
func locate(s string) (start, end int, ok bool) {
	i := strings.Index(s, Begin)
	if i < 0 {
		return 0, 0, false
	}
	j := strings.Index(s[i:], End)
	if j < 0 {
		return 0, 0, false
	}
	return i, i + j + len(End), true
}

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
