// Package guard is clauderig's PreToolUse policy engine for Claude Code. It turns
// a tool call into an allow-or-block decision so worktrees and PRs become the
// default path instead of an afterthought:
//
//   - EnterWorktree/ExitWorktree are denied — they move the *session's* working
//     directory, and Claude Code keys chat history to the folder path (see
//     internal/project). Moving cwd mid-session scrambles which ~/.claude/projects
//     bucket the conversation lands in. Make worktrees with `clauderig worktree`
//     (a sibling checkout, reviewed in its own VS Code window) instead.
//   - A `cd`/`pushd` that leaves the repo root is denied for the same reason — it
//     silently relocates the session. A subshell `(cd ... && ...)` is fine and is
//     the documented escape hatch, because it doesn't move the parent shell.
//   - On a protected base branch (main/master/trunk), code writes are denied so a
//     branch+worktree+PR is required — but low-risk paths (docs, README, root
//     config) pass, and an explicit override (CLAUDERIG_ALLOW_MAIN / a
//     .claude/allow-main sentinel) lets anything through.
//
// The engine is pure: callers in commands/guard.go gather the git facts (branch,
// root, what a commit would include) and hand them to Evaluate. Every uncertain
// case Defers — the guard never blocks more than it is sure about, so a bug can
// only fail open.
package guard

import (
	"encoding/json"
	"path"
	"path/filepath"
	"strings"
)

// Decision is the guard's verdict for one tool call.
type Decision int

const (
	// Defer leaves the call to Claude Code's normal permission flow (the guard
	// has no opinion). Emitted as *no* hook output, never an explicit "allow", so
	// the user's usual prompts/allowlist still apply.
	Defer Decision = iota
	// Deny blocks the call and shows Reason to Claude.
	Deny
)

// Result is a Decision plus the human/agent-facing Reason (empty when Defer).
type Result struct {
	Decision Decision
	Reason   string
}

// Request is the normalized tool call the guard reasons about.
type Request struct {
	Tool     string // e.g. "Edit", "Write", "Bash", "EnterWorktree"
	FilePath string // absolute target, for Edit/Write/NotebookEdit
	Command  string // shell command, for Bash
	Cwd      string // session working directory from the hook payload
}

// Env is the git/environment context the caller resolves for the Request.
type Env struct {
	InRepo bool   // Cwd is inside a git work tree
	Root   string // absolute repo top-level
	Home   string // user home, for ~ expansion in cd targets
	OnBase bool   // current branch is a protected base (main/master/trunk)
	// Override is set when the user has explicitly opted out of base-branch
	// protection (CLAUDERIG_ALLOW_MAIN truthy or a .claude/allow-main sentinel).
	Override bool
	// Committable lists the repo-relative paths a `git commit` in Command would
	// record (staged, plus tracked-modified when the command says -a). Only the
	// caller can know this; the guard classifies it. Empty when not a commit or
	// when undeterminable — in which case the commit check defers.
	Committable []string
}

// BaseBranches are the branch names the guard treats as protected.
var BaseBranches = map[string]bool{"main": true, "master": true, "trunk": true}

// Evaluate is the whole policy. It returns Deny only for cases it is certain
// about; everything else Defers.
func Evaluate(r Request, e Env) Result {
	// Worktree tools relocate the session regardless of repo state — always block.
	if r.Tool == "EnterWorktree" || r.Tool == "ExitWorktree" {
		return Result{Deny, worktreeReason}
	}
	if !e.InRepo {
		return Result{Defer, ""}
	}
	switch r.Tool {
	case "Bash":
		return evalBash(r, e)
	case "Edit", "Write", "NotebookEdit":
		return evalWrite(r, e)
	}
	return Result{Defer, ""}
}

func evalWrite(r Request, e Env) Result {
	if !e.OnBase || e.Override {
		return Result{Defer, ""}
	}
	rel, ok := repoRel(e.Root, r.FilePath)
	if !ok || LowRisk(rel) {
		return Result{Defer, ""} // outside this repo, or a doc/config edit
	}
	return Result{Deny, baseReason(rel)}
}

func evalBash(r Request, e Env) Result {
	// A session-level cd/pushd out of the repo silently moves the conversation.
	if target, outside := escapingCd(r.Command, r.Cwd, e.Root, e.Home); outside {
		return Result{Deny, cdReason(target)}
	}
	if !e.OnBase || e.Override {
		return Result{Defer, ""}
	}
	if isGitCommit(r.Command) {
		for _, f := range e.Committable {
			if !LowRisk(f) {
				return Result{Deny, commitReason(f)}
			}
		}
	}
	return Result{Defer, ""}
}

// LowRisk reports whether a repo-relative path is safe to change directly on a
// base branch without a PR: documentation, and top-level project config. Code is
// never low-risk. The path uses forward slashes (repoRel guarantees this).
func LowRisk(rel string) bool {
	rel = strings.TrimPrefix(rel, "./")
	if rel == "" {
		return false
	}
	base := path.Base(rel)
	// Documentation, anywhere in the tree.
	if strings.EqualFold(path.Ext(rel), ".md") || strings.EqualFold(path.Ext(rel), ".mdx") {
		return true
	}
	// Whole low-risk directories.
	switch firstSegment(rel) {
	case "docs", ".github":
		return true
	}
	// Top-level config and metadata only (a nested foo.yml may be code/fixtures).
	if !strings.Contains(rel, "/") {
		switch strings.ToLower(path.Ext(rel)) {
		case ".toml", ".yml", ".yaml", ".json", ".txt", ".ini", ".cfg", ".conf", ".editorconfig":
			return true
		}
		switch base {
		case ".gitignore", ".gitattributes", ".editorconfig", ".npmrc", ".nvmrc", ".dockerignore":
			return true
		}
		if strings.HasPrefix(strings.ToUpper(base), "LICENSE") {
			return true
		}
	}
	return false
}

// escapingCd scans command for a top-level `cd`/`pushd` whose target resolves
// outside root, returning that target. Segments are split on shell separators;
// a `(cd ... )` subshell starts with '(' so it never matches — that's the
// intended escape hatch for legitimately running a command elsewhere.
func escapingCd(command, cwd, root, home string) (target string, outside bool) {
	root = filepath.Clean(root)
	for _, seg := range splitSegments(command) {
		seg = strings.TrimSpace(seg)
		arg, ok := cdArg(seg)
		if !ok {
			continue
		}
		dest := resolveDir(arg, cwd, home)
		if dest == "" {
			continue // couldn't resolve (e.g. a variable) — don't guess
		}
		if !within(root, dest) {
			return arg, true
		}
	}
	return "", false
}

// cdArg returns the first argument of a leading cd/pushd segment. A bare `cd`
// (or `cd ~`) means "go home", which leaves any repo, so it reports "~".
func cdArg(seg string) (arg string, ok bool) {
	for _, kw := range []string{"cd", "pushd"} {
		if seg == kw {
			return "~", true
		}
		if strings.HasPrefix(seg, kw+" ") {
			rest := strings.TrimSpace(strings.TrimPrefix(seg, kw))
			rest = strings.TrimPrefix(rest, "-- ")
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				return "~", true
			}
			return strings.Trim(fields[0], `"'`), true
		}
	}
	return "", false
}

// resolveDir turns a cd target into an absolute, cleaned path, or "" if it can't
// (a variable/command substitution we shouldn't second-guess).
func resolveDir(arg, cwd, home string) string {
	if arg == "" || strings.ContainsAny(arg, "$`*?") {
		return ""
	}
	switch {
	case arg == "~":
		arg = home
	case strings.HasPrefix(arg, "~/"):
		arg = filepath.Join(home, arg[2:])
	}
	if arg == "" {
		return ""
	}
	if !filepath.IsAbs(arg) {
		if cwd == "" {
			return ""
		}
		arg = filepath.Join(cwd, arg)
	}
	return filepath.Clean(arg)
}

func isGitCommit(command string) bool {
	for _, seg := range splitSegments(command) {
		fields := strings.Fields(seg)
		for i := 0; i < len(fields)-1; i++ {
			if (fields[i] == "git" || strings.HasSuffix(fields[i], "/git")) && nextWord(fields[i+1:], "commit") {
				return true
			}
		}
	}
	return false
}

// nextWord reports whether want appears among args before any non-flag operand
// other than git's own global flags — good enough to spot `git ... commit`.
func nextWord(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func splitSegments(command string) []string {
	repl := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n", "&", "\n")
	return strings.Split(repl.Replace(command), "\n")
}

func repoRel(root, abs string) (string, bool) {
	if root == "" || abs == "" {
		return "", false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(abs))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func within(root, dir string) bool {
	return dir == root || strings.HasPrefix(dir, root+string(filepath.Separator))
}

func firstSegment(rel string) string {
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return rel
}

// --- hook I/O (pure helpers used by commands/guard.go) ---

// input is the subset of Claude Code's PreToolUse stdin payload we read.
type input struct {
	ToolName  string `json:"tool_name"`
	Cwd       string `json:"cwd"`
	ToolInput struct {
		FilePath string `json:"file_path"`
		Command  string `json:"command"`
	} `json:"tool_input"`
}

// Parse decodes a PreToolUse payload into a Request.
func Parse(stdin []byte) (Request, error) {
	var in input
	if err := json.Unmarshal(stdin, &in); err != nil {
		return Request{}, err
	}
	return Request{
		Tool:     in.ToolName,
		FilePath: in.ToolInput.FilePath,
		Command:  in.ToolInput.Command,
		Cwd:      in.Cwd,
	}, nil
}

// Output is the JSON to print on stdout for a Result, or nil to print nothing
// (Defer). A Deny is the documented PreToolUse deny envelope.
func Output(res Result) []byte {
	if res.Decision != Deny {
		return nil
	}
	b, _ := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": res.Reason,
		},
	})
	return b
}

const worktreeReason = "Worktree tools move this session's working directory, and Claude Code keys chat history to the folder path — moving it mid-session scrambles your VS Code chat. " +
	"Create an isolated worktree without moving this window: run `clauderig worktree new <branch>`. It makes a sibling checkout and opens it in a separate VS Code window for review; keep editing here by absolute path and run git via `git -C <worktree>`."

func baseReason(rel string) string {
	return "You're on a base branch (main/master) and `" + rel + "` is code, not docs/config. " +
		"Code changes need a branch + worktree + PR: run `clauderig worktree new <branch>`, edit the file under that worktree path, then open a PR. " +
		"Docs and root config may be edited on the base branch directly. To override for this change: set CLAUDERIG_ALLOW_MAIN=1 or `touch .claude/allow-main`."
}

func commitReason(rel string) string {
	return "This commit lands code (`" + rel + "`) directly on a base branch. " +
		"Use a branch + worktree + PR instead: `clauderig worktree new <branch>`. " +
		"Commits touching only docs/config are allowed; override with CLAUDERIG_ALLOW_MAIN=1 or `touch .claude/allow-main`."
}

func cdReason(target string) string {
	return "Refusing to `cd " + target + "` — that leaves the repo root and silently moves this session's working directory (and its chat history). " +
		"Run the command from here using an absolute path or `git -C <dir>`, or wrap it in a subshell `(cd " + target + " && …)` which doesn't move this shell. " +
		"To work in another tree, open it with `clauderig worktree open <branch>` in its own window."
}
