package guard

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const root = "/Users/john/Git/rigsmith"

func base(e Env) Env {
	e.InRepo = true
	e.Root = root
	e.Home = "/Users/john"
	return e
}

func TestEvaluate_DeniesWorktreeTools(t *testing.T) {
	for _, tool := range []string{"EnterWorktree", "ExitWorktree"} {
		// Denied even outside a repo — the tool moves cwd regardless.
		if got := Evaluate(Request{Tool: tool}, Env{}); got.Decision != Deny {
			t.Errorf("%s: Decision = %v, want Deny", tool, got.Decision)
		}
	}
}

func TestEvaluate_DefersOutsideRepo(t *testing.T) {
	r := Request{Tool: "Edit", FilePath: "/tmp/x/main.go"}
	if got := Evaluate(r, Env{InRepo: false}); got.Decision != Defer {
		t.Errorf("Decision = %v, want Defer", got.Decision)
	}
}

func TestEvalWrite_OnBase(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		env      Env
		wantDeny bool
	}{
		{"code on base", root + "/cli/main.go", base(Env{OnBase: true}), true},
		{"nested code on base", root + "/clauderig/internal/x/y.go", base(Env{OnBase: true}), true},
		{"markdown on base", root + "/README.md", base(Env{OnBase: true}), false},
		{"docs dir on base", root + "/docs/whatever.go", base(Env{OnBase: true}), false},
		{"root toml on base", root + "/netlify.toml", base(Env{OnBase: true}), false},
		{"github dir on base", root + "/.github/workflows/ci.yml", base(Env{OnBase: true}), false},
		{"nested yml is not low-risk", root + "/cli/testdata/fixture.yml", base(Env{OnBase: true}), true},
		{"code off base", root + "/cli/main.go", base(Env{OnBase: false}), false},
		{"code on base with override", root + "/cli/main.go", base(Env{OnBase: true, Override: true}), false},
		{"file outside repo", "/other/place/main.go", base(Env{OnBase: true}), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(Request{Tool: "Edit", FilePath: tt.file}, tt.env)
			if (got.Decision == Deny) != tt.wantDeny {
				t.Errorf("Decision = %v, wantDeny = %v (reason %q)", got.Decision, tt.wantDeny, got.Reason)
			}
		})
	}
}

func TestEvalBash_EscapingCd(t *testing.T) {
	// The cd-escape policy is evaluated with the host's filepath semantics (it
	// guards a live session on whatever OS it runs on). These fixtures are POSIX
	// paths — "/tmp/foo", "/etc", "~" → "/Users/john" — which aren't absolute under
	// Windows filepath, so the modeled escapes can't be reproduced there. clauderig
	// runs on macOS/Linux; assert the POSIX policy on a POSIX host.
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-path cd-escape fixtures; clauderig runs on macOS/Linux")
	}
	e := base(Env{})
	cwd := root
	tests := []struct {
		name     string
		command  string
		wantDeny bool
	}{
		{"cd into subdir is fine", "cd cli && go test ./...", false},
		{"cd abs subdir is fine", "cd " + root + "/cli && go build", false},
		{"cd to sibling repo denied", "cd ../buoy-server && ls", true},
		{"cd home denied", "cd && ls", true},
		{"cd tilde denied", "cd ~ && ls", true},
		{"cd abs outside denied", "cd /tmp/foo && ls", true},
		{"subshell cd is allowed", "(cd ../buoy-server && ls)", false},
		{"plain command", "go test ./...", false},
		{"variable target not guessed", "cd $HOME/x && ls", false},
		{"pushd outside denied", "pushd /etc && cat hosts", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(Request{Tool: "Bash", Command: tt.command, Cwd: cwd}, e)
			if (got.Decision == Deny) != tt.wantDeny {
				t.Errorf("command %q: Decision = %v, wantDeny = %v", tt.command, got.Decision, tt.wantDeny)
			}
		})
	}
}

func TestEvalBash_CommitOnBase(t *testing.T) {
	onBase := func(files ...string) Env {
		e := base(Env{OnBase: true})
		e.Committable = files
		return e
	}
	tests := []struct {
		name     string
		command  string
		env      Env
		wantDeny bool
	}{
		{"commit code on base", "git commit -m x", onBase("cli/main.go"), true},
		{"commit docs on base", "git commit -m x", onBase("README.md", "docs/a.md"), false},
		{"commit mixed on base", "git commit -am x", onBase("README.md", "cli/main.go"), true},
		{"commit code off base", "git commit -m x", base(Env{OnBase: false, Committable: []string{"cli/main.go"}}), false},
		{"commit code with override", "git commit -m x", base(Env{OnBase: true, Override: true, Committable: []string{"cli/main.go"}}), false},
		{"non-commit git on base", "git status", onBase("cli/main.go"), false},
		{"commit nothing determinable", "git commit -m x", onBase(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(Request{Tool: "Bash", Command: tt.command, Cwd: root}, tt.env)
			if (got.Decision == Deny) != tt.wantDeny {
				t.Errorf("command %q: Decision = %v, wantDeny = %v", tt.command, got.Decision, tt.wantDeny)
			}
		})
	}
}

func TestParseAndOutput(t *testing.T) {
	stdin := []byte(`{"hook_event_name":"PreToolUse","tool_name":"Edit","cwd":"/Users/john/Git/rigsmith","tool_input":{"file_path":"/Users/john/Git/rigsmith/cli/main.go"}}`)
	r, err := Parse(stdin)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tool != "Edit" || r.FilePath != root+"/cli/main.go" || r.Cwd != root {
		t.Fatalf("Parse = %+v", r)
	}
	// Deny → JSON envelope; Defer → no output.
	if out := Output(Result{Decision: Deny, Reason: "no"}); len(out) == 0 {
		t.Error("Deny should produce JSON output")
	}
	if out := Output(Result{Decision: Defer}); out != nil {
		t.Errorf("Defer should produce no output, got %q", out)
	}
}

func TestLowRisk(t *testing.T) {
	low := []string{"README.md", "docs/x.go", "docs/deep/y.ts", ".github/workflows/ci.yml", "netlify.toml", "LICENSE", "package.json", ".gitignore"}
	high := []string{"cli/main.go", "main.go", "internal/x.go", "cli/data/fixture.yml", "src/app.ts"}
	for _, p := range low {
		if !LowRisk(p) {
			t.Errorf("LowRisk(%q) = false, want true", p)
		}
	}
	for _, p := range high {
		if LowRisk(p) {
			t.Errorf("LowRisk(%q) = true, want false", p)
		}
	}
}

// Monitor executes its command in the same shell as Bash, so the same rules have
// to apply — otherwise it is a way around every one of them. Its WebSocket form
// carries no command and must stay inert.
func TestEvaluate_MonitorIsTreatedAsBash(t *testing.T) {
	root := t.TempDir()
	// `~` rather than a literal path: home is outside the repo on every platform,
	// where "/tmp" is only meaningful on one of them.
	env := Env{InRepo: true, Root: root, Home: t.TempDir(), OnBase: true}

	for _, tool := range []string{"Bash", "Monitor"} {
		got := Evaluate(Request{Tool: tool, Command: "cd ~ && echo hi", Cwd: root}, env)
		if got.Decision != Deny {
			t.Errorf("%s escaping cd: decision = %v, want Deny", tool, got.Decision)
		}
	}
	// The ws form: no command at all.
	if got := Evaluate(Request{Tool: "Monitor", Cwd: root}, env); got.Decision != Defer {
		t.Errorf("Monitor with no command: decision = %v, want Defer", got.Decision)
	}
	// And a harmless command still passes.
	if got := Evaluate(Request{Tool: "Monitor", Command: "tail -f build.log", Cwd: root}, env); got.Decision != Defer {
		t.Errorf("Monitor tail: decision = %v, want Defer", got.Decision)
	}
}

// Every tool in the registry must actually be acted on. The registry drives the
// PreToolUse matcher, so an entry the policy ignores means the hook pays to run
// for nothing — and, more importantly, the reverse (a policy case missing from
// the registry) is now impossible to express, because Evaluate asks the registry
// rather than repeating it.
func TestEveryRegisteredToolIsActedOn(t *testing.T) {
	root := t.TempDir()
	env := Env{InRepo: true, Root: root, Home: t.TempDir(), OnBase: true}
	for _, tool := range Tools() {
		req := Request{Tool: tool, Cwd: root}
		switch {
		case RunsCommand(tool):
			req.Command = "cd ~ && echo hi"
		case WritesFile(tool):
			req.FilePath = filepath.Join(root, "main.go")
		case TakesIsolation(tool):
			// Its rule is conditional on the input, the way the two above are:
			// the tool is fine, asking it to isolate itself is not.
			req.Isolation = "worktree"
		}
		if got := Evaluate(req, env); got.Decision != Deny {
			t.Errorf("%s: decision = %v, want Deny — it is in the matcher, so it should be governed", tool, got.Decision)
		}
	}
}

// The Agent tool takes isolation: "worktree" and creates the same
// .claude/worktrees/<name> checkout EnterWorktree would. The guard refused the
// tool by name and never looked at the input, so one was created in this repo on
// 6 September with the hook already installed — the branch that became #313.
func TestAgentWithWorktreeIsolationIsDenied(t *testing.T) {
	env := Env{InRepo: true, Root: "/repo"}
	got := Evaluate(Request{Tool: "Agent", Isolation: "worktree", Cwd: "/repo"}, env)
	if got.Decision != Deny {
		t.Fatalf("an isolated agent was %v, want Deny", got.Decision)
	}
	if !strings.Contains(got.Reason, "rig worktree new") {
		t.Errorf("the denial does not point at the sanctioned route: %q", got.Reason)
	}

	// An ordinary subagent is not a worktree and must still run. Denying every
	// Agent call would block the useful case to stop the rare one.
	for _, iso := range []string{"", "remote"} {
		if got := Evaluate(Request{Tool: "Agent", Isolation: iso, Cwd: "/repo"}, env); got.Decision != Defer {
			t.Errorf("Agent with isolation %q was %v, want Defer", iso, got.Decision)
		}
	}

	// Outside a repo too: the checkout is just as invisible there, and the
	// relocation rules above do not depend on repo state either.
	if got := Evaluate(Request{Tool: "Agent", Isolation: "worktree"}, Env{}); got.Decision != Deny {
		t.Errorf("outside a repo an isolated agent was %v, want Deny", got.Decision)
	}
}

// The hook only runs for tools the matcher names, so a rule for a tool missing
// from Tools() is a rule that never fires. This is how Monitor was missed once
// already.
func TestAgentIsInTheMatcher(t *testing.T) {
	var found bool
	for _, tool := range Tools() {
		if tool == "Agent" {
			found = true
		}
	}
	if !found {
		t.Error("Agent is not in Tools(), so the PreToolUse matcher will not fire for it")
	}
}

// The isolation flag is read off the payload, not just off a hand-built Request.
func TestParseReadsIsolation(t *testing.T) {
	req, err := Parse([]byte(`{"tool_name":"Agent","cwd":"/repo",
		"tool_input":{"isolation":"worktree","prompt":"go and do a thing"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.Isolation != "worktree" {
		t.Errorf("Isolation = %q, want it decoded from tool_input", req.Isolation)
	}
	if got := Evaluate(req, Env{InRepo: true, Root: "/repo"}); got.Decision != Deny {
		t.Errorf("the decoded request was %v, want Deny", got.Decision)
	}
}

// The other route to the same place: making one by hand.
func TestGitWorktreeAddUnderDotClaudeIsDenied(t *testing.T) {
	env := Env{InRepo: true, Root: "/repo"}
	denied := []string{
		"git worktree add .claude/worktrees/thing",
		// Path-qualified and Windows spellings: a rule that only knows the bare
		// word is a rule /usr/bin/git walks past.
		"/usr/bin/git worktree add .claude/worktrees/thing",
		`C:\Program Files\Git\cmd\git.exe worktree add .claude/worktrees/thing`,
		"git.exe worktree add .claude/worktrees/thing",
		"git worktree add -b feat .claude/worktrees/thing",
		"git -C /repo worktree add /repo/.claude/worktrees/thing",
		"echo hi && git worktree add .claude/worktrees/x",
		// Judged as git will resolve it, not as it was typed.
		"git worktree add .claude/tmp/../worktrees/thing",
		`git worktree add .claude\worktrees\thing`,
	}
	for _, cmd := range denied {
		if got := Evaluate(Request{Tool: "Bash", Command: cmd, Cwd: "/repo"}, env); got.Decision != Deny {
			t.Errorf("%q was %v, want Deny", cmd, got.Decision)
		}
	}

	// The place is what is wrong with it, and the place does not depend on
	// where the session happened to be standing. `git -C` reaches a repo from
	// outside one, and the repo gate used to let exactly that through.
	outside := Env{InRepo: false}
	if got := Evaluate(Request{Tool: "Bash", Command: "git -C /repo worktree add /repo/.claude/worktrees/thing", Cwd: "/tmp"}, outside); got.Decision != Deny {
		t.Errorf("from outside a repo: %v, want Deny", got.Decision)
	}

	// Cleaning one up has to keep working — blocking the remedy would be a poor
	// way to discourage the thing.
	allowed := []string{
		"git worktree list",
		"git worktree remove .claude/worktrees/thing",
		"rm -rf .claude/worktrees",
		"git worktree add ../repo-worktrees/thing",
		// Begins with the same letters and is a different directory. The rule
		// is about .claude/worktrees, not about anything spelled like it.
		"git worktree add .claude/worktrees-of-my-own/thing",
	}
	for _, cmd := range allowed {
		if got := Evaluate(Request{Tool: "Bash", Command: cmd, Cwd: "/repo"}, env); got.Decision != Defer {
			t.Errorf("%q was %v, want Defer — this is the way out, not the way in", cmd, got.Decision)
		}
	}
}
