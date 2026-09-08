package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// seedEmpty creates a bare repo whose tip carries no files at all. From the
// stackspace this is indistinguishable from an upstream it is not allowed to
// read: the ref resolves, and what comes back through the filter has no tree.
func (g *gitServer) seedEmpty(t *testing.T, name string) {
	t.Helper()
	bare := g.bare(t, name)
	work := t.TempDir()
	mustGitStack(t, work, "init", "-q", "-b", "main", work)
	mustGitStack(t, work, "config", "user.email", "t@t")
	mustGitStack(t, work, "config", "user.name", "t")
	mustGitStack(t, work, "commit", "-q", "--allow-empty", "-m", "nothing here")
	mustGitStack(t, work, "push", "-q", bare, "main")
}

// An import that fetches nothing used to be recorded as a success: the cursor
// was written, no tree appeared, and no import commit was made. After that
// `status` reported the member up to date against a directory that did not
// exist, and every later pull short-circuited on the cursor — so the stackspace
// never tried again and nothing said why.
//
// A private upstream the credential in use cannot read is how this is reached in
// practice: josh answers with an empty history rather than refusing, and the
// ls-remote before it passes because the credential authenticates, it just
// cannot see the repo. An upstream with an empty tip is the same shape without
// needing a forge to refuse anything.
func TestStackImportRefusesAnEmptyFetch(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") == "" {
		t.Skip("set RIG_STACK_E2E=1 to run the stack end-to-end flow")
	}
	proxy, err := stackJoshProxyBin(stackJoshVersion)
	if err != nil || stackJoshInstalled(proxy) != nil {
		t.Skip("no josh-proxy installed; run `rig stack doctor --fix` first")
	}

	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seedEmpty(t, "org/hollow")
	srv.bare(t, "me/hollow")

	ws := filepath.Join(work, "stackspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "init", "-q", "-b", "main")
	mustGitStack(t, ws, "config", "user.email", "t@t")
	mustGitStack(t, ws, "config", "user.name", "t")
	writeStackManifest(t, ws, fmt.Sprintf(`{
  "repos": {
    "hollow": { "upstream": %q, "fork": %q, "upstreamBranch": "main" }
  }
}`, srv.spec("org/hollow"), srv.spec("me/hollow")))
	mustGitStack(t, ws, "add", "-A")
	mustGitStack(t, ws, "commit", "-qm", "manifest")

	chdir(t, ws)
	head := strings.TrimSpace(mustGitStack(t, ws, "rev-parse", "HEAD"))

	err = runVerb(context.Background(), newStackInitCmd())
	if err == nil {
		t.Fatal("init reported success for an import that fetched no tree")
	}
	if !strings.Contains(err.Error(), "hollow/ tree") {
		t.Fatalf("the error does not say what was missing: %v", err)
	}

	// The three things the silent version left behind. The cursor is the one
	// that made it unrecoverable: with it written, a later pull believes this
	// revision is already here and never fetches again.
	manifest, readErr := os.ReadFile(filepath.Join(ws, "rig.stack.jsonc"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(manifest), "lastSync") {
		t.Errorf("a cursor was recorded for an import that produced nothing:\n%s", manifest)
	}
	if _, err := os.Stat(filepath.Join(ws, "hollow")); err == nil {
		t.Error("hollow/ exists after an import that fetched no tree")
	}
	if now := strings.TrimSpace(mustGitStack(t, ws, "rev-parse", "HEAD")); now != head {
		t.Errorf("history moved on a failed import: %s -> %s", head, now)
	}
}

// A member that was imported, removed, and is being taken back. Its filtered
// history is already an ancestor of this one, so the merge has nothing to do —
// and for a long time nothing else restored the directory either: `init`
// reported the import, wrote the cursor, and left no tree. Where the cursor was
// absent it failed instead inside `git commit --amend --no-edit: would make it
// empty`, amending a commit the import had no business touching.
func TestStackReimportsARemovedMember(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") == "" {
		t.Skip("set RIG_STACK_E2E=1 to run the stack end-to-end flow")
	}
	proxy, err := stackJoshProxyBin(stackJoshVersion)
	if err != nil || stackJoshInstalled(proxy) != nil {
		t.Skip("no josh-proxy installed; run `rig stack doctor --fix` first")
	}

	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seed(t, "org/libfoo", "libfoo")
	srv.bare(t, "me/libfoo")

	ws := filepath.Join(work, "stackspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "init", "-q", "-b", "main")
	mustGitStack(t, ws, "config", "user.email", "t@t")
	mustGitStack(t, ws, "config", "user.name", "t")
	manifest := fmt.Sprintf(`{
  "repos": {
    "libfoo": { "upstream": %q, "fork": %q, "upstreamBranch": "main" }
  }
}`, srv.spec("org/libfoo"), srv.spec("me/libfoo"))
	writeStackManifest(t, ws, manifest)
	mustGitStack(t, ws, "add", "-A")
	mustGitStack(t, ws, "commit", "-qm", "manifest")

	chdir(t, ws)
	ctx := context.Background()

	if err := runVerb(ctx, newStackInitCmd()); err != nil {
		t.Fatalf("first import: %v", err)
	}
	seeded := filepath.Join(ws, "libfoo", "src", "libfoo.txt")
	if _, err := os.Stat(seeded); err != nil {
		t.Fatalf("first import produced no tree: %v", err)
	}

	if err := runVerb(ctx, newStackRemoveCmd(), "libfoo", "--force"); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "libfoo")); err == nil {
		t.Fatal("rm left the directory behind; the test is not exercising a removal")
	}

	// rm takes the member out of the manifest too, so put it back — this is the
	// user editing the manifest to take the project on again.
	writeStackManifest(t, ws, manifest)
	mustGitStack(t, ws, "add", "-A")
	mustGitStack(t, ws, "commit", "-qm", "take libfoo back")

	if err := runVerb(ctx, newStackInitCmd()); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if _, err := os.Stat(seeded); err != nil {
		t.Fatalf("re-import restored no tree: %v", err)
	}
	// And it has to be a commit of its own: the merge made none to fold into,
	// so amending would have rewritten "take libfoo back".
	if subject := strings.TrimSpace(mustGitStack(t, ws, "log", "-1", "--format=%s")); !strings.Contains(subject, "libfoo") {
		t.Errorf("the re-import did not make its own commit; HEAD is %q", subject)
	}
	if got := strings.TrimSpace(mustGitStack(t, ws, "log", "-1", "--format=%s", "--", "rig.stack.jsonc")); got == "take libfoo back" {
		t.Error("the re-import amended the commit before it instead of making its own")
	}
}

// A cursor with no directory under it is what the old empty-import bug left
// behind, and what a stackspace carrying one still has. status and pull only
// ever consulted the cursor, so both called such a member current. Neither
// needs the engine to notice: the tip comes from ls-remote, the directory from
// HEAD, and the hint names the verb that rebuilds it.
func TestStackStatusAndPullNameAMissingPrefix(t *testing.T) {
	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seed(t, "org/libfoo", "libfoo")
	srv.bare(t, "me/libfoo")
	tip := strings.TrimSpace(mustGitStack(t, srv.path("org/libfoo"), "rev-parse", "main"))

	ws := filepath.Join(work, "stackspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "init", "-q", "-b", "main")
	mustGitStack(t, ws, "config", "user.email", "t@t")
	mustGitStack(t, ws, "config", "user.name", "t")
	writeStackManifest(t, ws, fmt.Sprintf(`{
  "repos": {
    "libfoo": { "upstream": %q, "fork": %q, "upstreamBranch": "main" }
  },
  "lastSync": { "libfoo": %q }
}`, srv.spec("org/libfoo"), srv.spec("me/libfoo"), tip))
	mustGitStack(t, ws, "add", "-A")
	mustGitStack(t, ws, "commit", "-qm", "manifest with a cursor and no tree")

	chdir(t, ws)
	ctx := context.Background()
	for _, verb := range []struct {
		name string
		cmd  func() *cobra.Command
	}{{"status", newStackStatusCmd}, {"pull", newStackPullCmd}} {
		out, err := runVerbOut(ctx, verb.cmd())
		if err != nil {
			t.Fatalf("%s: %v\n%s", verb.name, err, out)
		}
		if !strings.Contains(out, "no libfoo/ directory") || !strings.Contains(out, "rig stack setup") {
			t.Errorf("%s should say the directory is missing and what rebuilds it, got:\n%s", verb.name, out)
		}
		if strings.Contains(out, "up to date") || strings.Contains(out, "nothing to pull") {
			t.Errorf("%s called a member with no directory current:\n%s", verb.name, out)
		}
	}
}
