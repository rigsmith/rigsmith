// Package agentsmd manages codexrig's blocks inside AGENTS.md — Codex's
// instruction file, and the counterpart to Claude Code's CLAUDE.md.
//
// A managed block is delimited by HTML comments and rewritten in place, so
// running the command again updates the text rather than appending a second
// copy. Everything outside the markers is the user's and is never touched.
package agentsmd

import (
	"os"
	"strings"
)

// Section is one managed block.
type Section struct {
	Begin string
	End   string
	body  string
}

// Action says what Install or Uninstall did.
type Action string

const (
	Installed Action = "installed"
	Updated   Action = "updated"
	Unchanged Action = "unchanged"
	Removed   Action = "removed"
	Absent    Action = "absent"
)

// managedNotice opens every block, so somebody editing inside one finds out
// before their edit is overwritten rather than afterwards.
const managedNotice = "<!-- Managed by codexrig. Run `codexrig guide install` to update; edits inside this block are overwritten. -->"

// Worktree is the branch-and-PR discipline the guard enforces. The prose and
// the hook have to agree: an instruction file that describes a rule the guard
// does not apply is worse than no instruction file, because it gets believed.
var Worktree = Section{
	Begin: "<!-- BEGIN codexrig:worktree-discipline -->",
	End:   "<!-- END codexrig:worktree-discipline -->",
	body: managedNotice + `
## Branch & PR discipline (enforced by ` + "`codexrig guard`" + `)

A PreToolUse hook guards this repository. Work *with* it:

- **Don't write code on ` + "`main`/`master`/`trunk`" + `.** Make a branch and a worktree
  first: ` + "`rig worktree new <branch>`" + ` creates a sibling checkout you can edit by
  absolute path, then push and open a PR.
- **Docs and root config may go on the base branch directly** — ` + "`*.md`" + `, the
  ` + "`docs/`" + ` and ` + "`.github/`" + ` trees, and top-level config (` + "`*.toml`, `*.yml`, `*.json`" + `,
  ` + "`LICENSE`, `.gitignore`" + `). Everything else counts as code and needs a PR.
- **A patch is judged whole.** ` + "`apply_patch`" + ` may touch several files at once, and
  the guard looks at all of them — a patch that edits a README and three source
  files is a code change.
- **Override**, only when you must: ` + "`export CODEXRIG_ALLOW_MAIN=1`" + ` for this
  session, or ` + "`touch .codex/allow-main`" + ` for this repository.
`,
}

// Sync explains what codexrig backs up, for an agent that is asked about it.
var Sync = Section{
	Begin: "<!-- BEGIN codexrig:sync -->",
	End:   "<!-- END codexrig:sync -->",
	body: managedNotice + `
## Codex setup sync (` + "`codexrig`" + `)

This machine's Codex setup is backed up to a private git repo by ` + "`codexrig`" + `.

- ` + "`codexrig sync`" + ` captures config, instructions, skills, prompts and rules.
  ` + "`codexrig status`" + ` says how it is doing; ` + "`codexrig restore`" + ` brings it back.
- **Credentials never travel.** ` + "`auth.json`" + ` is excluded, and a value that looks
  like a token is redacted out of what is committed. A restored machine runs
  ` + "`codex login`" + ` once for itself.
- Session rollouts are carried only when ` + "`syncSessions`" + ` is on, and are never
  rewritten — a rollout is a conversation, not a config file.
`,
}

// Sections is every block `guide install` writes.
var Sections = []Section{Worktree, Sync}

// Block renders a section, newline-terminated.
func (s Section) Block() string {
	return s.Begin + "\n" + strings.TrimRight(s.body, "\n") + "\n" + s.End + "\n"
}

func (s Section) block() string { return strings.TrimRight(s.Block(), "\n") }

// locate finds the block's span, markers included. A Begin with no End is "not
// found", so a half-written block is appended to rather than surgically patched
// into something stranger.
func (s Section) locate(text string) (start, end int, ok bool) {
	i := strings.Index(text, s.Begin)
	if i < 0 {
		return 0, 0, false
	}
	j := strings.Index(text[i:], s.End)
	if j < 0 {
		return 0, 0, false
	}
	return i, i + j + len(s.End), true
}

// Install writes or refreshes the block.
func (s Section) Install(path string) (Action, error) {
	cur, err := readOrEmpty(path)
	if err != nil {
		return Absent, err
	}
	blk := s.block()
	if start, end, ok := s.locate(cur); ok {
		if cur[start:end] == blk {
			// Byte-identical: write nothing at all, so the file's mtime does
			// not move and nothing downstream thinks it changed.
			return Unchanged, nil
		}
		return Updated, write(path, cur[:start]+blk+cur[end:])
	}
	sep := "\n\n"
	switch {
	case cur == "":
		sep = ""
	case strings.HasSuffix(cur, "\n\n"):
		sep = ""
	case strings.HasSuffix(cur, "\n"):
		sep = "\n"
	}
	return Installed, write(path, cur+sep+blk)
}

// Uninstall removes the block and the gap it leaves.
func (s Section) Uninstall(path string) (Action, error) {
	cur, err := readOrEmpty(path)
	if err != nil {
		return Absent, err
	}
	start, end, ok := s.locate(cur)
	if !ok {
		return Absent, nil
	}
	rest := cur[end:]
	rest = strings.TrimLeft(rest, "\n")
	head := strings.TrimRight(cur[:start], "\n")
	out := head
	if head != "" && rest != "" {
		out += "\n\n"
	}
	out += rest
	return Removed, write(path, out)
}

// Present reports whether the block is there.
func (s Section) Present(path string) (bool, error) {
	cur, err := readOrEmpty(path)
	if err != nil {
		return false, err
	}
	_, _, ok := s.locate(cur)
	return ok, nil
}

// InstallAll writes every section, reporting the strongest thing that happened.
func InstallAll(path string) (Action, error) {
	roll := Unchanged
	for _, s := range Sections {
		act, err := s.Install(path)
		if err != nil {
			return roll, err
		}
		roll = merge(roll, act)
	}
	return roll, nil
}

// UninstallAll removes every section.
func UninstallAll(path string) (Action, error) {
	removed := false
	for _, s := range Sections {
		act, err := s.Uninstall(path)
		if err != nil {
			return Absent, err
		}
		if act == Removed {
			removed = true
		}
	}
	if removed {
		return Removed, nil
	}
	return Absent, nil
}

// AllPresent is false when ANY section is missing, so a later-added block is
// treated as something to install rather than as already done.
func AllPresent(path string) (bool, error) {
	for _, s := range Sections {
		ok, err := s.Present(path)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// Blocks renders every section, for a preview.
func Blocks() string {
	parts := make([]string, 0, len(Sections))
	for _, s := range Sections {
		parts = append(parts, s.block())
	}
	return strings.Join(parts, "\n\n") + "\n"
}

func merge(roll, act Action) Action {
	switch {
	case roll == Installed || act == Installed:
		return Installed
	case roll == Updated || act == Updated:
		return Updated
	default:
		return Unchanged
	}
}

func readOrEmpty(path string) (string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func write(path, content string) error {
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
