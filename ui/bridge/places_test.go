package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, rel, body string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// sidecar is what Desktop writes about a session it knows of.
func sidecar(id, cliID, title, cwd string, at time.Time) string {
	return `{"sessionId":"` + id + `","cliSessionId":"` + cliID + `","title":"` + title +
		`","cwd":"` + cwd + `","branch":"main","isArchived":false,"lastActivityAt":` +
		itoa(at.UnixMilli()) + `}`
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// The whole point of this window: a Desktop keeps a record of a session whose
// conversation lives somewhere else entirely. That record is what says where the
// session went, so the link it carries has to survive into the listing.
func TestDesktopItemsCarryTheLinkToTheTranscript(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, codeSessions, "acct-1111", "ws-2222")
	writeFile(t, base, codeSessions+"/acct-1111/ws-2222/local_aaa.json",
		sidecar("local_aaa", "424f8e2f-9b1e-4074-b1b5-ac1fc09b67df", "Session export/import",
			"/Users/john/Git/rigsmith", time.Now().Add(-2*time.Hour)))
	writeFile(t, base, codeSessions+"/acct-1111/ws-2222/scheduled-tasks.json", `{}`)

	items := desktopItems(dir)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	var found bool
	for _, it := range items {
		switch it.Kind {
		case ItemSidecar:
			found = true
			if it.Label != "Session export/import" {
				t.Errorf("label = %q, want the sidecar's own title", it.Label)
			}
			if it.CLISession != "424f8e2f-9b1e-4074-b1b5-ac1fc09b67df" {
				t.Errorf("cliSession = %q — the link to the transcript was dropped", it.CLISession)
			}
			if it.Cwd == "" || it.Branch != "main" {
				t.Errorf("cwd/branch missing: %+v", it)
			}
		case ItemConfig:
			if it.Label != "scheduled-tasks.json" {
				t.Errorf("unexpected config item %q", it.Label)
			}
		}
	}
	if !found {
		t.Error("no sidecar in the listing")
	}
}

// A sidecar that will not parse is still evidence that the session was here,
// which is the fact being looked for. It must not be dropped.
func TestUnparseableSidecarStillListed(t *testing.T) {
	base := t.TempDir()
	writeFile(t, base, codeSessions+"/a/w/local_broken.json", `{not json`)
	items := desktopItems(filepath.Join(base, codeSessions, "a", "w"))
	if len(items) != 1 || items[0].Kind != ItemSidecar {
		t.Fatalf("got %+v, want the broken sidecar listed anyway", items)
	}
	if items[0].Label != "local_broken.json" {
		t.Errorf("label = %q, want the filename as the fallback", items[0].Label)
	}
}

// Two accounts here really do have workspaces with the same id, so neither uuid
// identifies a workspace on its own. The label is taken from what was being
// worked on instead, because that is the handle someone actually has.
func TestWorkspaceLabelledByItsWork(t *testing.T) {
	base := t.TempDir()
	home, _ := os.UserHomeDir()
	writeFile(t, base, codeSessions+"/acct-1/ws-same/local_a.json",
		sidecar("local_a", "cli-1", "Something", filepath.Join(home, "Git", "rigsmith"), time.Now()))
	writeFile(t, base, codeSessions+"/acct-2/ws-same/local_b.json",
		sidecar("local_b", "cli-2", "Other", "", time.Now().Add(-time.Hour)))

	groups := desktopWorkspaces(location{base: base, kind: "desktop"})
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want one per account", len(groups))
	}
	var labelled, fallback int
	for _, g := range groups {
		switch {
		case strings.HasPrefix(g.Label, "~"):
			labelled++
			if g.Note == "" {
				t.Error("a labelled workspace lost the uuids it is called on disk")
			}
		case strings.Contains(g.Label, "/"):
			fallback++ // no cwd to read: the uuids are all there is
		default:
			t.Errorf("unexpected label %q", g.Label)
		}
	}
	if labelled != 1 || fallback != 1 {
		t.Errorf("labelled=%d fallback=%d, want one of each", labelled, fallback)
	}
}

// A project slug is the working directory with its separators flattened. Turning
// it back is what makes the list readable, but it is lossy — so the slug stays
// on the row rather than being replaced by the guess.
func TestCLIProjectsReadAsPaths(t *testing.T) {
	base := t.TempDir()
	writeFile(t, base, cliProjectsDir+"/-Users-john-Git-rigsmith/aaa.jsonl", "{}\n")
	writeFile(t, base, cliProjectsDir+"/-Users-john-Git-rigsmith/aaa/tool-results/x.txt", "x")

	groups := cliProjects(location{base: base, kind: "cli"})
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	if groups[0].Label != "/Users/john/Git/rigsmith" {
		t.Errorf("label = %q, want the decoded path", groups[0].Label)
	}
	if groups[0].Note != "-Users-john-Git-rigsmith" {
		t.Errorf("note = %q, want the slug kept beside the guess", groups[0].Note)
	}

	items := cliItems(filepath.Join(base, cliProjectsDir, "-Users-john-Git-rigsmith"))
	var kinds []string
	for _, it := range items {
		kinds = append(kinds, it.Kind)
	}
	// The transcript, and the directory of tool output beside it — which is the
	// part people are surprised to find missing after a restore.
	if len(items) != 2 || !contains(kinds, ItemTranscript) || !contains(kinds, ItemAux) {
		t.Errorf("items = %v, want a transcript and its aux dir", kinds)
	}
}

// The group id arrives from the window, so it is input, and it is joined onto a
// path. It must not be able to climb out of the store it names.
func TestGroupDirRefusesToLeaveTheStore(t *testing.T) {
	loc := location{base: t.TempDir(), kind: "cli"}
	for _, bad := range []string{"", "..", "../../etc", "a/../../..", "/etc/passwd"} {
		if _, err := groupDir(loc, bad); err == nil {
			t.Errorf("groupDir accepted %q", bad)
		}
	}
	if _, err := groupDir(loc, "-Users-john-Git"); err != nil {
		t.Errorf("groupDir refused an ordinary slug: %v", err)
	}
}

// A store that is not there and a store with nothing in it render the same and
// mean opposite things — one has not been synced here, the other is genuinely
// empty. The listing has to tell them apart.
func TestAbsentStoreIsNotAnEmptyOne(t *testing.T) {
	present := describeStore(location{id: "x", base: t.TempDir(), kind: "desktop"})
	if !present.Present {
		t.Error("an existing but empty store reported as absent")
	}
	absent := describeStore(location{id: "y", base: filepath.Join(t.TempDir(), "nope"), kind: "desktop"})
	if absent.Present {
		t.Error("a missing store reported as present")
	}
}

// The config a store carries is the part no session listing shows, and the
// reason a profile with no sessions can still be worth restoring.
func TestStoreConfigListsWhatAProfileCarries(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	writeFile(t, data, "claude_desktop_config.json", `{"mcpServers":{}}`)
	writeFile(t, data, "git-worktrees.json", `{}`)
	writeFile(t, root, "profile.json", `{"name":"relatecpa"}`)

	got := storeConfig(location{base: data, kind: "profile", profile: "relatecpa"})
	var names []string
	for _, c := range got {
		names = append(names, c.Label)
		if c.Kind != ItemConfig {
			t.Errorf("%s listed as %s", c.Label, c.Kind)
		}
	}
	for _, want := range []string{"claude_desktop_config.json", "git-worktrees.json", "profile.json"} {
		if !contains(names, want) {
			t.Errorf("%s missing from %v", want, names)
		}
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
