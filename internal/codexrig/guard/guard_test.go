package guard

import (
	"encoding/json"
	"strings"
	"testing"
)

func onBase() Env { return Env{InRepo: true, Root: "/repo", OnBase: true} }

func TestAPatchTouchingAnyCodeFileIsRefused(t *testing.T) {
	// The case a per-file guard gets wrong: Codex applies one patch that
	// touches several files, and a guard that stops at the first sees the
	// README and approves the rest.
	patch := `*** Begin Patch
*** Update File: README.md
@@
-old
+new
*** Update File: internal/thing/server.go
@@
-x
+y
*** End Patch`
	res := Evaluate(Request{Tool: "apply_patch", Cwd: "/repo", Command: patch}, onBase())
	if res.Decision != Deny {
		t.Fatal("a patch that rewrites a .go file on a base branch was allowed")
	}
	if !strings.Contains(res.Reason, "server.go") {
		t.Errorf("the refusal names %q rather than the file that caused it", res.Reason)
	}
}

func TestAPatchOfOnlyDocsIsAllowed(t *testing.T) {
	patch := `*** Begin Patch
*** Update File: README.md
*** Add File: docs/design.md
*** End Patch`
	if Evaluate(Request{Tool: "apply_patch", Cwd: "/repo", Command: patch}, onBase()).Decision != Defer {
		t.Error("documentation should be editable on a base branch")
	}
}

func TestPatchPathsReadsEveryAddressingForm(t *testing.T) {
	patch := `*** Begin Patch
*** Add File: a.go
*** Update File: b.go
*** Delete File: c.go
*** Move to: d.go
*** End Patch`
	got := PatchPaths(patch)
	want := []string{"a.go", "b.go", "c.go", "d.go"}
	if len(got) != len(want) {
		t.Fatalf("PatchPaths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("PatchPaths = %v, want %v", got, want)
		}
	}
}

func TestASingleFileEditIsJudgedToo(t *testing.T) {
	deny := Evaluate(Request{Tool: "Write", Cwd: "/repo", FilePath: "/repo/main.go"}, onBase())
	if deny.Decision != Deny {
		t.Error("a code write on a base branch was allowed")
	}
	allow := Evaluate(Request{Tool: "Write", Cwd: "/repo", FilePath: "/repo/CHANGELOG.md"}, onBase())
	if allow.Decision != Defer {
		t.Error("a markdown write on a base branch was refused")
	}
}

func TestARelativePathIsResolvedAgainstTheToolsCwd(t *testing.T) {
	res := Evaluate(Request{Tool: "Write", Cwd: "/repo/internal", FilePath: "thing.go"}, onBase())
	if res.Decision != Deny {
		t.Error("a relative path was not placed inside the repository")
	}
}

func TestAPathOutsideTheRepositoryIsNotThisRepositorysBusiness(t *testing.T) {
	res := Evaluate(Request{Tool: "Write", Cwd: "/repo", FilePath: "/elsewhere/thing.go"}, onBase())
	if res.Decision != Defer {
		t.Error("a file outside the repo was judged by the repo's branch policy")
	}
}

func TestNothingIsRefusedOffABaseBranch(t *testing.T) {
	env := Env{InRepo: true, Root: "/repo", OnBase: false}
	if Evaluate(Request{Tool: "Write", Cwd: "/repo", FilePath: "/repo/main.go"}, env).Decision != Defer {
		t.Error("a branch is exactly where code changes belong")
	}
}

func TestTheOverrideIsHonoured(t *testing.T) {
	env := Env{InRepo: true, Root: "/repo", OnBase: true, Override: true}
	if Evaluate(Request{Tool: "Write", Cwd: "/repo", FilePath: "/repo/main.go"}, env).Decision != Defer {
		t.Error("an explicit override was ignored")
	}
}

func TestBypassPermissionsIsRespected(t *testing.T) {
	// The user has already told Codex to stop asking. Being the one thing that
	// still refuses just teaches them to remove the hook.
	req := Request{Tool: "Write", Cwd: "/repo", FilePath: "/repo/main.go", PermissionMode: "bypassPermissions"}
	if Evaluate(req, onBase()).Decision != Defer {
		t.Error("the guard argued with a mode the user explicitly chose")
	}
}

func TestAHiddenWorktreeIsRefusedEvenOffABaseBranch(t *testing.T) {
	env := Env{InRepo: true, Root: "/repo", OnBase: false}
	res := Evaluate(Request{Tool: "Bash", Cwd: "/repo", Command: "git worktree add .codex/worktrees/x -b x"}, env)
	if res.Decision != Deny {
		t.Error("a worktree under .codex/worktrees was allowed")
	}
	// Removing one has to keep working: blocking the cleanup is a poor way to
	// discourage them.
	rm := Evaluate(Request{Tool: "Bash", Cwd: "/repo", Command: "git worktree remove .codex/worktrees/x"}, env)
	if rm.Decision != Defer {
		t.Error("cleaning up a hidden worktree was blocked")
	}
}

func TestTheMatcherAndTheSwitchCannotDrift(t *testing.T) {
	// One list builds both. They were separate in the sibling tool, and a tool
	// added to one but not the other is a rule that silently stops applying.
	m := Matcher()
	for _, tool := range Tools() {
		if !strings.Contains(m, tool) {
			t.Errorf("%q is inspected but is not in the hook matcher, so the guard is never called for it", tool)
		}
	}
	for _, want := range []string{"apply_patch", "Bash", "Write"} {
		if !strings.Contains(m, want) {
			t.Errorf("the matcher does not cover %q", want)
		}
	}
}

func TestParseReadsCodexsOwnPayloadShape(t *testing.T) {
	// Field names read out of the binary's embedded schema, not assumed from
	// the sibling tool's — they agree, and that is worth pinning rather than
	// relying on.
	payload := `{"session_id":"s","transcript_path":"/t.jsonl","cwd":"/repo","hook_event_name":"PreToolUse",
	  "model":"gpt-6","permission_mode":"default","tool_use_id":"t1","turn_id":"u1",
	  "tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Update File: a.go\n*** End Patch"}}`
	req, err := Parse([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if req.Tool != "apply_patch" || req.Cwd != "/repo" || req.PermissionMode != "default" {
		t.Fatalf("req = %+v", req)
	}
	if !strings.Contains(req.Command, "Update File: a.go") {
		t.Errorf("the patch document did not come through: %q", req.Command)
	}
}

func TestAToolInputThatIsNotAnObjectDoesNotBreakThePayload(t *testing.T) {
	// tool_input is schema'd as "any", so some tool will one day send a string.
	req, err := Parse([]byte(`{"tool_name":"Bash","cwd":"/repo","tool_input":"ls"}`))
	if err != nil {
		t.Fatalf("a string tool_input made the whole payload unreadable: %v", err)
	}
	if req.Tool != "Bash" {
		t.Errorf("req = %+v", req)
	}
}

func TestOutputIsSilentUnlessItDenies(t *testing.T) {
	// Silence leaves Codex's own approval rules in charge; an explicit allow
	// would override the sandbox and the user's settings.
	if b := Output(Result{Decision: Defer}); b != nil {
		t.Errorf("a deferral printed %q", b)
	}
	b := Output(Result{Decision: Deny, Reason: "because"})
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	hs, ok := doc["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("output = %s", b)
	}
	if hs["hookEventName"] != "PreToolUse" || hs["permissionDecision"] != "deny" || hs["permissionDecisionReason"] != "because" {
		t.Errorf("output = %s", b)
	}
	// The schema sets additionalProperties:false — anything extra is rejected.
	if len(doc) != 1 || len(hs) != 3 {
		t.Errorf("the output carries fields the schema does not allow: %s", b)
	}
}
