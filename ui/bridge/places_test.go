package bridge

import (
	"os"
	"path/filepath"
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

// sidecarIn is a sidecar whose origin folder differs from where it ran, which
// is what a worktree session looks like.
func sidecarIn(id, cliID, title, origin, cwd string, at time.Time) string {
	return `{"sessionId":"` + id + `","cliSessionId":"` + cliID + `","title":"` + title +
		`","originCwd":"` + origin + `","cwd":"` + cwd + `","branch":"main","lastActivityAt":` +
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
func TestSidecarsCarryTheLinkToTheTranscript(t *testing.T) {
	base := t.TempDir()
	writeFile(t, base, codeSessions+"/acct-1111/ws-2222/local_aaa.json",
		sidecar("local_aaa", "424f8e2f-9b1e-4074-b1b5-ac1fc09b67df", "Session export/import",
			"/Users/john/Git/rigsmith", time.Now().Add(-2*time.Hour)))

	refs := scanSidecars(filepath.Join(base, codeSessions))
	if len(refs) != 1 {
		t.Fatalf("got %d records, want 1", len(refs))
	}
	it := refs[0].item
	if it.Label != "Session export/import" {
		t.Errorf("label = %q, want the sidecar's own title", it.Label)
	}
	if it.CLISession != "424f8e2f-9b1e-4074-b1b5-ac1fc09b67df" {
		t.Errorf("cliSession = %q — the link to the transcript was dropped", it.CLISession)
	}
	if it.Branch != "main" {
		t.Errorf("branch missing: %+v", it)
	}
}

// A session Claude Desktop has dropped from its own sidebar still has a record
// here. Someone hunting a session they can no longer see is looking for exactly
// that, so it is listed — and marked, because "it is gone" and "it is here and
// deleted" are different answers.
func TestDeletedSessionsAreListedAndMarked(t *testing.T) {
	base := t.TempDir()
	writeFile(t, base, codeSessions+"/a/w/local_live.json",
		sidecar("local_live", "cli-1", "Still here", "/Users/john/Git", time.Now()))
	// A real tombstone is the deletion timestamp and nothing else.
	writeFile(t, base, codeSessions+"/a/w/deleted_gone.json", itoa(time.Now().UnixMilli()))
	// Not a session record at all, and must not be mistaken for one.
	writeFile(t, base, codeSessions+"/a/w/scheduled-tasks.json", `{}`)

	refs := scanSidecars(filepath.Join(base, codeSessions))
	if len(refs) != 2 {
		t.Fatalf("got %d records, want the live one and the deleted one", len(refs))
	}
	var deleted int
	for _, r := range refs {
		if r.item.Deleted {
			deleted++
			if r.item.Session != "gone" {
				t.Errorf("deleted record lost its id: %q", r.item.Session)
			}
		}
	}
	if deleted != 1 {
		t.Errorf("marked %d records deleted, want 1", deleted)
	}
}

// A sidecar that will not parse is still evidence that the session was here,
// which is the fact being looked for. It must not be dropped.
func TestUnparseableSidecarStillListed(t *testing.T) {
	base := t.TempDir()
	writeFile(t, base, codeSessions+"/a/w/local_broken.json", `{not json`)
	refs := scanSidecars(filepath.Join(base, codeSessions))
	if len(refs) != 1 {
		t.Fatalf("got %+v, want the broken sidecar listed anyway", refs)
	}
	if refs[0].item.Label != "local_broken.json" {
		t.Errorf("label = %q, want the filename as the fallback", refs[0].item.Label)
	}
	if refs[0].folder != unknownFolder {
		t.Errorf("folder = %q, want the unrecorded bucket", refs[0].folder)
	}
}

// Desktop files sessions under the folder they were opened in, and shows that
// folder as the heading in its own sidebar. Grouping by it is what makes this
// window and the app agree about where a session lives. The <account>/<workspace>
// uuids in the path are not that: they are opaque, they repeat across accounts,
// and nobody has ever remembered one.
func TestSessionsGroupByTheFolderTheyWereOpenedIn(t *testing.T) {
	base := t.TempDir()
	home, _ := os.UserHomeDir()
	git := filepath.Join(home, "Git")
	// Two accounts, same workspace id, same folder: one group, not three.
	writeFile(t, base, codeSessions+"/acct-1/ws-same/local_a.json",
		sidecarIn("local_a", "cli-1", "One", git, filepath.Join(git, "rigsmith"), time.Now()))
	writeFile(t, base, codeSessions+"/acct-2/ws-same/local_b.json",
		sidecarIn("local_b", "cli-2", "Two", git, git, time.Now().Add(-time.Hour)))
	// A different folder is a different group.
	writeFile(t, base, codeSessions+"/acct-1/ws-other/local_c.json",
		sidecarIn("local_c", "cli-3", "Three", filepath.Join(git, "tweed"), git, time.Now()))

	groups := desktopFolders(location{base: base, kind: "desktop"})
	byLabel := map[string]int{}
	for _, g := range groups {
		byLabel[g.Label] = g.Items
	}
	if byLabel["~/Git"] != 2 {
		t.Errorf("~/Git holds %d sessions, want the two opened there: %v", byLabel["~/Git"], byLabel)
	}
	if byLabel["~/Git/tweed"] != 1 {
		t.Errorf("~/Git/tweed holds %d, want 1: %v", byLabel["~/Git/tweed"], byLabel)
	}
}

// A project slug is the working directory with its separators flattened. Turning
// it back is what makes the list readable, but it is lossy — so the slug stays
// on the row rather than being replaced by the guess.
func TestCLIProjectsReadAsPaths(t *testing.T) {
	base := t.TempDir()
	writeFile(t, base, cliProjectsDir+"/-Users-john-Git-rigsmith/aaa.jsonl", "{}\n")
	writeFile(t, base, cliProjectsDir+"/-Users-john-Git-rigsmith/aaa/tool-results/x.txt", "x")

	groups := cliProjects(location{base: base, kind: "cli"}, false)
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

// A project slug has had every separator and dot flattened to a dash, so it
// cannot be reversed: "-Users-john-Git-XTerm-NET" reads back as .../XTerm/NET
// and the directory is XTerm.NET. The real spelling is recovered from a
// transcript's own record of where it ran — and proved against the slug rather
// than trusted, because a transcript can sit under a parent project's slug with
// its own cwd several levels further down.
func TestProjectPathRecoversTheRealDirectoryName(t *testing.T) {
	dir := t.TempDir()
	tr := filepath.Join(dir, "s.jsonl")
	writeFile(t, dir, "s.jsonl",
		`{"type":"bridge-session","sessionId":"x"}`+"\n"+
			`{"type":"user","cwd":"/Users/john/Git/XTerm.NET/.claude/worktrees/wt-1"}`+"\n")

	got := projectPath("-Users-john-Git-XTerm-NET", tr)
	if got != "/Users/john/Git/XTerm.NET" {
		t.Errorf("projectPath = %q, want the real directory the slug was made from", got)
	}
	// A slug that no prefix of the cwd accounts for must not be answered with a
	// guess: a path that looks right and is not is worse than none.
	if got := projectPath("-somewhere-else", tr); got != "" {
		t.Errorf("projectPath invented %q for a slug the cwd cannot explain", got)
	}
}

// Spaces are flattened to dashes too, and are the case where the guess reads
// most convincingly wrong.
func TestProjectPathRecoversSpaces(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "s.jsonl", `{"cwd":"/Users/john/Dropbox/File Cabinet/2026"}`+"\n")
	got := projectPath("-Users-john-Dropbox-File-Cabinet-2026", filepath.Join(dir, "s.jsonl"))
	if got != "/Users/john/Dropbox/File Cabinet/2026" {
		t.Errorf("projectPath = %q, want the spaces back", got)
	}
}

// slugOf has to match how Claude Code names these directories, or nothing above
// will ever line up.
func TestSlugOfMatchesClaudeCodesNaming(t *testing.T) {
	for path, want := range map[string]string{
		"/Users/john/Git/rigsmith":                   "-Users-john-Git-rigsmith",
		"/Users/john/Git/XTerm.NET":                  "-Users-john-Git-XTerm-NET",
		"/Users/john/Git/rigsmith/.claude/worktrees": "-Users-john-Git-rigsmith--claude-worktrees",
		"/Users/john/Dropbox/File Cabinet":           "-Users-john-Dropbox-File-Cabinet",
	} {
		if got := slugOf(path); got != want {
			t.Errorf("slugOf(%q) = %q, want %q", path, got, want)
		}
	}
}

// A deleted record is a tombstone: the whole file is the millisecond it was
// deleted at, and the session id is in its name. It has no title and no folder,
// so it gets a place of its own rather than being filed under "unknown" beside
// records that merely failed to say where they were.
func TestDeletedTombstonesGetTheirOwnPlace(t *testing.T) {
	base := t.TempDir()
	when := time.Now().Add(-48 * time.Hour).Truncate(time.Millisecond)
	writeFile(t, base, codeSessions+"/a/w/deleted_7ae8c132-f0a1-4bc0-8a00-985f9be72eac.json",
		itoa(when.UnixMilli()))

	refs := scanSidecars(filepath.Join(base, codeSessions))
	if len(refs) != 1 {
		t.Fatalf("got %d records, want the tombstone", len(refs))
	}
	r := refs[0]
	if r.folder != deletedFolder {
		t.Errorf("folder = %q, want %q", r.folder, deletedFolder)
	}
	if !r.item.Deleted {
		t.Error("tombstone not marked deleted")
	}
	if r.item.Session != "7ae8c132-f0a1-4bc0-8a00-985f9be72eac" {
		t.Errorf("session = %q, want the id from the filename", r.item.Session)
	}
	if !r.item.When.Equal(when) {
		t.Errorf("when = %v, want the timestamp the file holds (%v)", r.item.When, when)
	}
}

// A Desktop store holds more than one account's sessions side by side, and the
// app only ever shows one account at a time. Merging them puts sessions you do
// not recognise inside a folder you do — which is exactly how a session goes
// missing from a listing that is technically showing everything.
func TestSessionsAreSplitByAccount(t *testing.T) {
	base := t.TempDir()
	home, _ := os.UserHomeDir()
	git := filepath.Join(home, "Git")
	writeFile(t, base, codeSessions+"/acct-aaa/ws/local_a.json",
		sidecarIn("local_a", "cli-1", "Mine", git, git, time.Now()))
	writeFile(t, base, codeSessions+"/acct-bbb/ws/local_b.json",
		sidecarIn("local_b", "cli-2", "Theirs", git, git, time.Now()))

	groups := desktopFolders(location{base: base, kind: "desktop"})
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want one per account: %+v", len(groups), groups)
	}
	accounts := map[string]bool{}
	for _, g := range groups {
		if g.Label != "~/Git" {
			t.Errorf("label = %q, want the folder", g.Label)
		}
		if g.Items != 1 {
			t.Errorf("%s holds %d sessions, want its own one", g.Account, g.Items)
		}
		accounts[g.Account] = true
	}
	if len(accounts) != 2 {
		t.Errorf("accounts = %v, want the two kept apart", accounts)
	}
}
