package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
