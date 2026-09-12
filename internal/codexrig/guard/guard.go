// Package guard is the PreToolUse hook: it refuses a change that would put code
// straight onto a base branch, and asks for a branch and a PR instead.
//
// It is a narrower guard than clauderig's, deliberately. clauderig also refuses
// the tools that relocate a session, because Claude Code keys its chat history to
// the working directory and moving it mid-session scrambles the conversation.
// Codex has no such tools and records the working directory per thread, so those
// refusals have no counterpart here and inventing one would be theatre.
//
// What IS new is the shape of a change. Claude Code edits one file per call, with
// the path in tool_input.file_path. Codex applies a PATCH — `apply_patch` carries
// a document in tool_input.command that may add, update, delete and move several
// files at once — so a guard that reads one path per call sees the first file and
// waves the rest through.
//
// The whole thing fails open. Every error path defers, so a bug here can only
// ever let something past, never block work the user needed to do.
package guard

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Decision is the verdict.
type Decision int

const (
	// Defer emits NOTHING, which leaves Codex's own approval rules in charge.
	// The guard never emits an allow: approving a call would override the
	// sandbox and the user's own settings, which is not this hook's business.
	Defer Decision = iota
	Deny
)

// Result is a verdict with the sentence explaining it.
type Result struct {
	Decision Decision
	Reason   string
}

// Request is what the hook was told, reduced to what the guard uses.
type Request struct {
	Tool string
	Cwd  string
	// Command is tool_input.command: a shell line for Bash, a patch document
	// for apply_patch.
	Command string
	// FilePath is tool_input.file_path, for the single-file editing tools.
	FilePath string
	// PermissionMode is Codex's own mode for the turn. bypassPermissions means
	// the user has already said to stop asking, and the guard respects that
	// rather than being the one thing that still argues.
	PermissionMode string
}

// Env is the repository state the verdict is made against.
type Env struct {
	InRepo bool
	Root   string
	OnBase bool
	// Override is the user's explicit "yes, on this branch, on purpose".
	Override bool
}

// BaseBranches are the branch names that mean "the thing everyone builds on".
var BaseBranches = map[string]bool{"main": true, "master": true, "trunk": true}

// patchTools apply a multi-file patch document.
var patchTools = []string{"apply_patch", "ApplyPatch"}

// writeTools edit exactly one file, named in tool_input.file_path.
var writeTools = []string{"Edit", "Write", "NotebookEdit", "edit_file", "write_file"}

// commandTools run a shell line.
var commandTools = []string{"Bash", "exec_command", "shell"}

// Tools is every tool this guard wants to see, for the hook matcher.
//
// One list, used both to build the matcher and to decide what to inspect. They
// were separate in clauderig, and a tool added to the matcher but not to the
// switch — or the reverse — is a rule that silently stops applying. The matcher
// is the only thing standing between the guard and never being called.
func Tools() []string {
	out := append([]string{}, writeTools...)
	out = append(out, patchTools...)
	out = append(out, commandTools...)
	return out
}

// Matcher is Tools() as a Codex hook matcher.
func Matcher() string { return strings.Join(Tools(), "|") }

func in(list []string, tool string) bool {
	for _, t := range list {
		if t == tool {
			return true
		}
	}
	return false
}

// Evaluate decides.
func Evaluate(r Request, e Env) Result {
	// The user has already told Codex to stop asking. Being the one thing that
	// still refuses would just teach them to uninstall the hook.
	if r.PermissionMode == "bypassPermissions" {
		return Result{Decision: Defer}
	}
	if !e.InRepo {
		return Result{Decision: Defer}
	}
	if in(commandTools, r.Tool) && addsHiddenWorktree(r.Command) {
		return Result{Deny, hiddenWorktreeReason}
	}
	if !e.OnBase || e.Override {
		return Result{Decision: Defer}
	}

	var touched []string
	switch {
	case in(patchTools, r.Tool):
		touched = PatchPaths(r.Command)
	case in(writeTools, r.Tool):
		if r.FilePath != "" {
			touched = []string{r.FilePath}
		}
	case in(commandTools, r.Tool):
		// A commit is the moment code lands, so that is where a shell line is
		// judged. Everything else a shell does is the user's business.
		if !isGitCommit(r.Command) {
			return Result{Decision: Defer}
		}
		return Result{Decision: Defer} // see commitNote
	}

	for _, p := range touched {
		rel, ok := repoRel(e.Root, p, r.Cwd)
		if !ok {
			continue // outside the repo: not this repository's discipline
		}
		if !LowRisk(rel) {
			return Result{Deny, baseReason(rel)}
		}
	}
	return Result{Decision: Defer}
}

// commitNote records a deliberate gap rather than an oversight.
//
// clauderig refuses a `git commit` on a base branch by listing the staged files
// and checking each one. That needs a git call on every shell command, inside a
// hook that runs before every tool use, and Codex normalises far more shell
// activity through this path than Claude Code does. The write and patch tools
// already catch the change BEFORE it is made, which is the better moment, so the
// commit check is left out rather than made expensive.
const commitNote = "the write is refused before the commit, so the commit itself is not inspected"

var _ = commitNote

// LowRisk reports whether a path is documentation or root configuration —
// things it is reasonable to change on a base branch directly.
func LowRisk(rel string) bool {
	rel = filepath.ToSlash(rel)
	ext := strings.ToLower(filepath.Ext(rel))
	if ext == ".md" || ext == ".mdx" {
		return true
	}
	first, _, _ := strings.Cut(rel, "/")
	if first == "docs" || first == ".github" {
		return true
	}
	// Top level only. A nested foo.yml is as likely to be a fixture or a
	// workflow's input as it is to be configuration.
	if strings.Contains(rel, "/") {
		return false
	}
	switch ext {
	case ".toml", ".yml", ".yaml", ".json", ".txt", ".ini", ".cfg", ".conf", ".editorconfig":
		return true
	}
	base := filepath.Base(rel)
	switch base {
	case ".gitignore", ".gitattributes", ".editorconfig", ".npmrc", ".nvmrc", ".dockerignore":
		return true
	}
	return strings.HasPrefix(strings.ToUpper(base), "LICENSE")
}

// patchHeader matches the file-addressing lines of an apply_patch document.
var patchHeader = regexp.MustCompile(`(?m)^\*\*\*\s+(Add File|Update File|Delete File|Move to):\s*(.+?)\s*$`)

// PatchPaths returns every file an apply_patch document touches.
//
// All of them, not the first: a patch that updates a README and rewrites three
// source files is a code change, and a guard that stops at the README approves
// it. A move counts on both sides, since the destination is where the code ends
// up.
func PatchPaths(patch string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range patchHeader.FindAllStringSubmatch(patch, -1) {
		p := strings.TrimSpace(m[2])
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// repoRel places a path inside the repository, resolving a relative one against
// the tool's working directory.
func repoRel(root, p, cwd string) (string, bool) {
	if !filepath.IsAbs(p) {
		base := cwd
		if base == "" {
			base = root
		}
		p = filepath.Join(base, p)
	}
	rel, err := filepath.Rel(root, filepath.Clean(p))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// hiddenWorktreeDir is where an agent harness puts a checkout nothing later
// cleans up.
const hiddenWorktreeDir = ".codex/worktrees"

var gitWorktreeAdd = regexp.MustCompile(`\bgit\b[^;&|]*\bworktree\s+add\b`)

// addsHiddenWorktree reports whether a shell line creates a worktree under the
// harness's own directory.
//
// Only `worktree add`. Removing one that is already there has to keep working —
// blocking the cleanup would be a poor way to discourage them.
func addsHiddenWorktree(command string) bool {
	if !gitWorktreeAdd.MatchString(command) {
		return false
	}
	norm := strings.ReplaceAll(command, `\`, "/")
	return strings.Contains(norm, hiddenWorktreeDir)
}

var gitCommit = regexp.MustCompile(`\bgit\b[^;&|]*\bcommit\b`)

func isGitCommit(command string) bool { return gitCommit.MatchString(command) }

// --- the hook wire format ------------------------------------------------
//
// Both halves below are Codex's, read out of the binary's own embedded schemas
// rather than assumed from Claude Code's — they happen to agree, and that is
// worth knowing rather than relying on.

type hookInput struct {
	ToolName       string          `json:"tool_name"`
	Cwd            string          `json:"cwd"`
	PermissionMode string          `json:"permission_mode"`
	ToolInput      json.RawMessage `json:"tool_input"`
}

// Parse reads a PreToolUse payload.
func Parse(stdin []byte) (Request, error) {
	var in hookInput
	if err := json.Unmarshal(stdin, &in); err != nil {
		return Request{}, err
	}
	req := Request{Tool: in.ToolName, Cwd: in.Cwd, PermissionMode: in.PermissionMode}
	// tool_input is schema'd as `true` — any value at all — so it is decoded
	// defensively and separately: a tool whose input is a string rather than an
	// object must not make the whole payload unreadable.
	var ti struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
		Patch    string `json:"patch"`
		Input    string `json:"input"`
	}
	if len(in.ToolInput) > 0 {
		_ = json.Unmarshal(in.ToolInput, &ti)
	}
	req.Command = firstNonEmpty(ti.Command, ti.Patch, ti.Input)
	req.FilePath = firstNonEmpty(ti.FilePath, ti.Path)
	return req, nil
}

// Output renders a verdict. A Defer renders NOTHING: silence leaves Codex's own
// rules in charge, and an explicit allow would override the sandbox and the
// user's settings.
func Output(res Result) []byte {
	if res.Decision != Deny {
		return nil
	}
	// The schema sets additionalProperties:false, so only the fields it names
	// may appear.
	doc := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": res.Reason,
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil
	}
	return b
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func baseReason(rel string) string {
	return "You're on a base branch (main/master/trunk) and `" + rel + "` is code, not docs or root config. " +
		"Code changes want a branch and a PR: `rig worktree new <branch>` makes a sibling checkout, " +
		"edit the file under that path, then open a PR. Docs and top-level config may be edited on the base " +
		"branch directly. To override for this change: set CODEXRIG_ALLOW_MAIN=1 or `touch .codex/allow-main`."
}

const hiddenWorktreeReason = "That creates a worktree under .codex/worktrees, which `rig worktree list` cannot see " +
	"and nothing later cleans up. Use `rig worktree new <branch>`: a sibling checkout, visible to the tooling and " +
	"reaped by `rig prune`. Removing one that is already there is fine and is not blocked."
