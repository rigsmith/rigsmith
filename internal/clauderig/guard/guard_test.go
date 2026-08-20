package guard

import (
	"runtime"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/hooks"
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
	env := Env{InRepo: true, Root: root, Home: "/home/u", OnBase: true}

	for _, tool := range []string{"Bash", "Monitor"} {
		got := Evaluate(Request{Tool: tool, Command: "cd /tmp && echo hi", Cwd: root}, env)
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

// The matcher is what the hook fires on: a tool the policy handles but the
// matcher omits is never consulted at all, which is exactly how Monitor slipped
// through. Keep the two in step.
func TestGuardPlansMatcherCoversEveryToolEvaluateHandles(t *testing.T) {
	matcher := ""
	for _, p := range hooks.GuardPlans() {
		if p.Event == "PreToolUse" {
			matcher = p.Matcher
		}
	}
	listed := map[string]bool{}
	for _, name := range strings.Split(matcher, "|") {
		listed[name] = true
	}
	for _, tool := range []string{"Edit", "Write", "NotebookEdit", "Bash", "Monitor", "EnterWorktree", "ExitWorktree"} {
		if !listed[tool] {
			t.Errorf("%s is acted on by Evaluate but missing from the PreToolUse matcher — the hook would never fire for it", tool)
		}
	}
}
