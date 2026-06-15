package forge

import (
	"strings"
	"testing"
)

// argAfter returns the argument following flag in the call, or "".
func argAfter(c *recordedCall, flag string) string {
	for i, a := range c.args {
		if a == flag && i+1 < len(c.args) {
			return c.args[i+1]
		}
	}
	return ""
}

func githubIssuesCfg() IssuesConfig {
	return IssuesConfig{Comment: "Released in {{version}}", Close: true}
}

func TestRunIssues_CommentsAndCloses(t *testing.T) {
	runner := &recordingRunner{} // gh auth ok, gh issue view returns "" (no marker)
	msgs := []string{"fix: a bug\n\nFixes #1", "feat: thing (see #2)"}

	ok, message := RunIssues(msgs, selGitHub, githubIssuesCfg(), "core@1.0.0", t.TempDir(), runner.run, nil)
	if !ok {
		t.Fatalf("ok=false: %q", message)
	}

	// #1 is closing ⇒ commented AND closed.
	c1 := findCall(runner, "gh", "comment", "1")
	if c1 == nil {
		t.Fatalf("no comment on #1; calls: %v", runner.calls)
	}
	body := argAfter(c1, "--body")
	if !strings.Contains(body, "core@1.0.0") {
		t.Errorf("comment body missing version: %q", body)
	}
	if !strings.Contains(body, "<!-- shiprig-released: core@1.0.0 -->") {
		t.Errorf("comment body missing dedupe marker: %q", body)
	}
	if findCall(runner, "gh", "close", "1") == nil {
		t.Errorf("expected #1 to be closed; calls: %v", runner.calls)
	}

	// #2 is only a mention ⇒ commented but NOT closed.
	if findCall(runner, "gh", "comment", "2") == nil {
		t.Errorf("expected a comment on #2; calls: %v", runner.calls)
	}
	if findCall(runner, "gh", "close", "2") != nil {
		t.Errorf("#2 is a mention, must not be closed; calls: %v", runner.calls)
	}
	if message != "Issues: 2 commented, 1 closed." {
		t.Errorf("summary = %q", message)
	}
}

func TestRunIssues_MarkerDedupeSkipsComment(t *testing.T) {
	runner := &recordingRunner{
		responder: func(call recordedCall) (string, error) {
			// gh issue view returns a comments payload already carrying the marker.
			if call.has("view") {
				return `{"comments":[{"body":"<!-- shiprig-released: core@1.0.0 -->"}]}`, nil
			}
			return "", nil
		},
	}
	ok, _ := RunIssues([]string{"Fixes #1"}, selGitHub, githubIssuesCfg(), "core@1.0.0", t.TempDir(), runner.run, nil)
	if !ok {
		t.Fatal("ok=false")
	}
	if findCall(runner, "gh", "comment", "1") != nil {
		t.Errorf("comment should be skipped when marker already present; calls: %v", runner.calls)
	}
	// Close is still attempted (idempotent on the forge side).
	if findCall(runner, "gh", "close", "1") == nil {
		t.Errorf("expected #1 to still be closed; calls: %v", runner.calls)
	}
}

func TestRunIssues_CommentOnly_NoClose(t *testing.T) {
	runner := &recordingRunner{}
	cfg := IssuesConfig{Comment: "Released {{version}}", Close: false}

	ok, _ := RunIssues([]string{"Fixes #1"}, selGitHub, cfg, "v1", t.TempDir(), runner.run, nil)
	if !ok {
		t.Fatal("ok=false")
	}
	if findCall(runner, "gh", "comment", "1") == nil {
		t.Error("expected a comment")
	}
	if findCall(runner, "gh", "close", "1") != nil {
		t.Error("close disabled, should not close")
	}
}

func TestRunIssues_NonGithubForge_Skips(t *testing.T) {
	runner := &recordingRunner{} // glab auth ok ⇒ gitlab selected, but no issue provider yet
	ok, message := RunIssues([]string{"Fixes #1"}, Selection{Forge: "gitlab"}, githubIssuesCfg(), "v1", t.TempDir(), runner.run, nil)
	if !ok {
		t.Fatalf("ok=false: %q", message)
	}
	for _, c := range runner.calls {
		if c.has("comment") || c.has("close") {
			t.Fatalf("no issue mutations expected for gitlab: %v", c)
		}
	}
	if message != "Issue automation not yet supported for gitlab; skipped." {
		t.Errorf("message = %q", message)
	}
}

func TestRunIssues_TagsOnly_Skips(t *testing.T) {
	runner := &recordingRunner{}
	ok, message := RunIssues([]string{"Fixes #1"}, selNone, githubIssuesCfg(), "v1", t.TempDir(), runner.run, nil)
	if !ok {
		t.Fatalf("ok=false: %q", message)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("none forge should run nothing; calls: %v", runner.calls)
	}
	if message != "Forge releases disabled; tags are handled by the publish/tag steps." {
		t.Errorf("message = %q", message)
	}
}

func TestRunIssues_NothingToDo(t *testing.T) {
	runner := &recordingRunner{}
	ok, message := RunIssues([]string{"chore: no refs here"}, selGitHub, githubIssuesCfg(), "v1", t.TempDir(), runner.run, nil)
	if !ok {
		t.Fatalf("ok=false: %q", message)
	}
	if message != "Issues: nothing to comment or close." {
		t.Errorf("message = %q", message)
	}
}
