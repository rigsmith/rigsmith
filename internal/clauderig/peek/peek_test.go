package peek

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
)

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("git"); err != nil {
		os.Exit(0) // no git — skip the package rather than fail
	}
	os.Exit(m.Run())
}

// syncCommit is one machine's sync: the files it added, under the commit
// subject clauderig actually writes.
type syncCommit struct {
	machine string
	files   map[string]string
	// delete removes paths, standing in for a retention prune.
	delete []string
}

// remoteRepo builds a repo whose origin/main carries the given sync commits in
// order, mirroring the synced layout. It returns the local clone — which has
// fetched but deliberately NOT merged, the state peek exists for.
func remoteRepo(t *testing.T, commits ...syncCommit) *gitrepo.Repo {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}

	bare := filepath.Join(root, "remote.git")
	run(root, "init", "-q", "--bare", bare)
	wc := filepath.Join(root, "wc")
	run(root, "clone", "-q", bare, wc)
	run(wc, "config", "user.email", "t@t")
	run(wc, "config", "user.name", "t")

	for _, c := range commits {
		for rel, body := range c.files {
			p := filepath.Join(wc, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for _, rel := range c.delete {
			if err := os.Remove(filepath.Join(wc, filepath.FromSlash(rel))); err != nil {
				t.Fatal(err)
			}
		}
		run(wc, "add", "-A")
		run(wc, "commit", "-qm", "clauderig sync: "+c.machine)
	}
	run(wc, "push", "-q", "origin", "HEAD:main")

	local := filepath.Join(root, "local")
	run(root, "clone", "-q", "--no-checkout", bare, local)
	run(local, "config", "user.email", "t@t")
	run(local, "config", "user.name", "t")

	repo, err := gitrepo.Open(ctx, local)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func transcript(prompt string) string {
	return `{"type":"user","message":{"role":"user","content":"` + prompt + `"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":"sure"}}` + "\n"
}

// The core claim: a peer's sessions are readable from the remote with no merge.
// The local clone here has never checked anything out, let alone merged.
func TestListReadsWithoutMerging(t *testing.T) {
	ctx := context.Background()
	repo := remoteRepo(t,
		syncCommit{machine: "Pro16", files: map[string]string{
			"cli/projects/-Users-john-Git-a/aaaaaaaa-0000-0000-0000-000000000000.jsonl": transcript("pro question"),
		}},
		syncCommit{machine: "Air13", files: map[string]string{
			"cli/projects/-Users-john-Git-b/bbbbbbbb-0000-0000-0000-000000000000.jsonl": transcript("air question"),
		}},
	)

	sessions, err := List(ctx, repo, DefaultRef)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2: %+v", len(sessions), sessions)
	}

	// Newest sync first.
	if sessions[0].Machine != "Air13" {
		t.Errorf("newest-first ordering broken: %+v", sessions)
	}
	byID := map[string]Session{}
	for _, s := range sessions {
		byID[s.ID] = s
	}
	pro := byID["aaaaaaaa-0000-0000-0000-000000000000"]
	if pro.Machine != "Pro16" {
		t.Errorf("attribution wrong: %+v", pro)
	}
	if pro.Slug != "-Users-john-Git-a" {
		t.Errorf("slug = %q", pro.Slug)
	}
	if pro.SyncedAt.IsZero() {
		t.Error("sync time not parsed")
	}
}

// Attribution comes from the sync commit subject, which is the only record of
// which machine a session came from — the repo merges everyone into one tree.
func TestAttributionIgnoresNonSyncCommits(t *testing.T) {
	if got := machineFromSubject("clauderig sync: Johns-MacBook-Air13"); got != "Johns-MacBook-Air13" {
		t.Errorf("machineFromSubject = %q", got)
	}
	// A merge or hand-made commit yields no machine rather than a guess.
	for _, subject := range []string{
		"Merge remote-tracking branch 'origin/main'",
		"clauderig: squashed history",
		"",
	} {
		if got := machineFromSubject(subject); got != "" {
			t.Errorf("machineFromSubject(%q) = %q, want empty", subject, got)
		}
	}
}

// A session touched by several syncs is attributed to the most recent one, and
// listed once.
func TestListAttributesToLatestSync(t *testing.T) {
	ctx := context.Background()
	const rel = "cli/projects/-p/cccccccc-0000-0000-0000-000000000000.jsonl"
	repo := remoteRepo(t,
		syncCommit{machine: "Pro16", files: map[string]string{rel: transcript("first")}},
		syncCommit{machine: "Air13", files: map[string]string{rel: transcript("first") + transcript("resumed")}},
	)

	sessions, err := List(ctx, repo, DefaultRef)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("session listed %d times, want once", len(sessions))
	}
	if sessions[0].Machine != "Air13" {
		t.Errorf("want the latest sync's machine, got %q", sessions[0].Machine)
	}
}

func TestFindByPrefix(t *testing.T) {
	sessions := []Session{
		{ID: "abcdef00-1111-2222-3333-444444444444"},
		{ID: "abcdef99-5555-6666-7777-888888888888"},
		{ID: "ffffffff-0000-0000-0000-000000000000"},
	}

	if s, err := Find(sessions, "ffffffff"); err != nil || s.ID != sessions[2].ID {
		t.Errorf("unique prefix failed: %v %v", s, err)
	}
	if s, err := Find(sessions, sessions[0].ID); err != nil || s.ID != sessions[0].ID {
		t.Errorf("exact id failed: %v %v", s, err)
	}
	// An ambiguous prefix must say so rather than silently pick one.
	if _, err := Find(sessions, "abcdef"); err == nil ||
		!strings.Contains(err.Error(), "matches 2 sessions") {
		t.Errorf("ambiguous prefix error = %v", err)
	}
	if _, err := Find(sessions, "nope"); err == nil {
		t.Error("unknown id was accepted")
	}
}

func TestTitlesFromBlobs(t *testing.T) {
	ctx := context.Background()
	repo := remoteRepo(t, syncCommit{machine: "Pro16", files: map[string]string{
		"cli/projects/-p/dddddddd-0000-0000-0000-000000000000.jsonl": transcript("how do I wire the tray icon"),
	}})

	sessions, err := List(ctx, repo, DefaultRef)
	if err != nil {
		t.Fatal(err)
	}
	titled := Titles(ctx, repo, DefaultRef, sessions)
	if titled[0].Title != "how do I wire the tray icon" {
		t.Fatalf("title = %q", titled[0].Title)
	}
	// Titles must not mutate the caller's slice.
	if sessions[0].Title != "" {
		t.Error("Titles wrote back into the input slice")
	}
}

func TestMaterializeWritesTranscript(t *testing.T) {
	ctx := context.Background()
	const id = "eeeeeeee-0000-0000-0000-000000000000"
	repo := remoteRepo(t, syncCommit{machine: "Air13", files: map[string]string{
		"cli/projects/-Users-john-Git-demo/" + id + ".jsonl": transcript("bring me over"),
	}})
	sessions, err := List(ctx, repo, DefaultRef)
	if err != nil {
		t.Fatal(err)
	}

	projects := filepath.Join(t.TempDir(), "projects")
	got, err := Materialize(ctx, repo, DefaultRef, sessions[0], projects, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "bring me over") {
		t.Fatalf("transcript content wrong:\n%s", body)
	}
	if got.Bytes == 0 {
		t.Error("Bytes not reported")
	}
	if filepath.Base(got.Path) != id+".jsonl" {
		t.Errorf("landed at %s", got.Path)
	}
}

// The additive guarantee. The local copy may be a session that's still running,
// or one that has moved on since the remote's snapshot — overwriting either
// loses turns nobody asked to lose. This is the same lesson as restore's
// live-session guard, so it is enforced here rather than assumed.
func TestMaterializeNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	const id = "eeeeeeee-0000-0000-0000-000000000000"
	repo := remoteRepo(t, syncCommit{machine: "Air13", files: map[string]string{
		"cli/projects/-demo/" + id + ".jsonl": transcript("the remote snapshot"),
	}})
	sessions, err := List(ctx, repo, DefaultRef)
	if err != nil {
		t.Fatal(err)
	}

	projects := filepath.Join(t.TempDir(), "projects")
	local := filepath.Join(projects, "-demo", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		t.Fatal(err)
	}
	const liveBody = "local copy with turns the remote has never seen\n"
	if err := os.WriteFile(local, []byte(liveBody), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Materialize(ctx, repo, DefaultRef, sessions[0], projects, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want ErrExists, got %v", err)
	}
	if got.Path != local {
		t.Errorf("reported path = %q, want the local file", got.Path)
	}
	if body, _ := os.ReadFile(local); string(body) != liveBody {
		t.Fatalf("local transcript was overwritten:\n%s", body)
	}
}

// A session from a machine with a different home lands under a folder that means
// nothing here unless the slug is translated, the same way restore does it.
func TestMaterializeRewritesSlugForThisMachine(t *testing.T) {
	ctx := context.Background()
	const id = "abcdabcd-0000-0000-0000-000000000000"
	repo := remoteRepo(t, syncCommit{machine: "Air13", files: map[string]string{
		"cli/projects/-Users-john-Git-demo/" + id + ".jsonl": transcript("hello"),
	}})
	sessions, err := List(ctx, repo, DefaultRef)
	if err != nil {
		t.Fatal(err)
	}

	man := &manifest.Manifest{
		Schema: 1, SourceOS: pathmap.OSMacOS,
		Projects: map[string]manifest.Project{
			"-Users-john-Git-demo": {Template: "$HOME/Git/demo", Cwd: "/Users/john/Git/demo"},
		},
	}
	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}

	projects := filepath.Join(t.TempDir(), "projects")
	got, err := Materialize(ctx, repo, DefaultRef, sessions[0], projects, man, jane.Resolver())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Rewrote {
		t.Fatalf("slug was not rewritten: %+v", got)
	}
	if got.Slug != "-Users-jane-Git-demo" {
		t.Fatalf("slug = %q, want jane's layout", got.Slug)
	}
	if _, err := os.Stat(got.Path); err != nil {
		t.Fatalf("transcript not written: %v", err)
	}
}

// With no manifest entry the source slug is carried across unchanged — the
// "bring it over anyway" rule, matching restore.
func TestMaterializeFallsBackToSourceSlug(t *testing.T) {
	ctx := context.Background()
	repo := remoteRepo(t, syncCommit{machine: "Air13", files: map[string]string{
		"cli/projects/-unmapped/12341234-0000-0000-0000-000000000000.jsonl": transcript("x"),
	}})
	sessions, err := List(ctx, repo, DefaultRef)
	if err != nil {
		t.Fatal(err)
	}
	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}

	got, err := Materialize(ctx, repo, DefaultRef, sessions[0],
		filepath.Join(t.TempDir(), "projects"), &manifest.Manifest{Projects: map[string]manifest.Project{}}, jane.Resolver())
	if err != nil {
		t.Fatal(err)
	}
	if got.Rewrote || got.Slug != "-unmapped" {
		t.Fatalf("unmapped project should keep its slug: %+v", got)
	}
}

func TestMachinesAndFilter(t *testing.T) {
	sessions := []Session{
		{ID: "1", Machine: "Air13"},
		{ID: "2", Machine: "Pro16"},
		{ID: "3", Machine: "Air13"},
		{ID: "4", Machine: ""}, // a merge commit's file — unattributable
	}

	if got := Machines(sessions); len(got) != 2 || got[0] != "Air13" || got[1] != "Pro16" {
		t.Errorf("Machines = %v", got)
	}
	if got := FilterMachine(sessions, "air13"); len(got) != 2 {
		t.Errorf("case-insensitive filter returned %d", len(got))
	}
	if got := FilterMachine(sessions, ""); len(got) != 4 {
		t.Errorf("empty filter should pass everything, got %d", len(got))
	}
}

// The same session can live at more than one path — a project directory
// renamed, or two machines whose slugs differ for the same cwd. It is still one
// session, and listing it twice inflates every count derived from the listing.
// Real data produced the same id four times before this was fixed.
func TestListDedupesBySessionID(t *testing.T) {
	ctx := context.Background()
	const id = "0a0a0a0a-0000-0000-0000-000000000000"
	repo := remoteRepo(t,
		syncCommit{machine: "Pro16", files: map[string]string{
			"cli/projects/-old-slug/" + id + ".jsonl": transcript("before the move"),
		}},
		syncCommit{machine: "Air13", files: map[string]string{
			"cli/projects/-new-slug/" + id + ".jsonl": transcript("after the move"),
		}},
	)

	sessions, err := List(ctx, repo, DefaultRef)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("session listed %d times, want once: %+v", len(sessions), sessions)
	}
	// The newest sync wins, so the surviving path is the current one.
	if sessions[0].Slug != "-new-slug" || sessions[0].Machine != "Air13" {
		t.Errorf("kept the stale copy: %+v", sessions[0])
	}

	// Find must therefore report one match, not two.
	if _, err := Find(sessions, id[:8]); err != nil {
		t.Errorf("prefix lookup should be unambiguous now: %v", err)
	}
}

// Claude Code writes a session's sub-agent output under the session's own
// directory. Those are not sessions, and because IDFromTranscriptRel maps them
// to their parent's id, one appearing in a newer sync could shadow the real
// transcript — the viewer would render an agent's output as the conversation.
func TestListExcludesSubagentTranscripts(t *testing.T) {
	ctx := context.Background()
	const id = "77777777-0000-0000-0000-000000000000"
	repo := remoteRepo(t,
		syncCommit{machine: "Pro16", files: map[string]string{
			"cli/projects/-p/" + id + ".jsonl": transcript("the real conversation"),
		}},
		// A later sync carrying only sub-agent output for the same session.
		syncCommit{machine: "Air13", files: map[string]string{
			"cli/projects/-p/" + id + "/subagents/agent-abc.jsonl": transcript("agent chatter"),
		}},
	)

	sessions, err := List(ctx, repo, DefaultRef)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want just the real one: %+v", len(sessions), sessions)
	}
	if sessions[0].Slug == "subagents" {
		t.Fatal("a sub-agent file was listed as a session")
	}
	// And the surviving path is the conversation, not the agent output.
	if !strings.HasSuffix(sessions[0].Path, id+".jsonl") {
		t.Fatalf("wrong path won: %s", sessions[0].Path)
	}
	titled := Titles(ctx, repo, DefaultRef, sessions)
	if titled[0].Title != "the real conversation" {
		t.Fatalf("viewer would show the wrong transcript: %q", titled[0].Title)
	}
}

func TestIsSessionTranscript(t *testing.T) {
	yes := []string{
		"cli/projects/-p/aaa.jsonl",
		"projects/-Users-john-Git-x/bbb.jsonl",
	}
	no := []string{
		"cli/projects/-p/aaa/subagents/agent-x.jsonl",
		"cli/projects/-p/aaa/subagents/agent-x.meta.json",
		"cli/projects/-p/aaa.meta.json",
		"cli/projects/-p",
		"desktop/whatever.jsonl",
		"",
	}
	for _, rel := range yes {
		if !isSessionTranscript(rel) {
			t.Errorf("%q should be a session transcript", rel)
		}
	}
	for _, rel := range no {
		if isSessionTranscript(rel) {
			t.Errorf("%q should not be a session transcript", rel)
		}
	}
}

// Retention prunes aged transcripts, so history mentions paths the tree no
// longer has. Listing those offers sessions that cannot be opened — `git show`
// fails on them — and their titles come back blank because the blob is gone.
// On real data 52 of 744 logged paths were in exactly this state.
func TestListExcludesPrunedSessions(t *testing.T) {
	ctx := context.Background()
	const keep = "11112222-0000-0000-0000-000000000000"
	const pruned = "33334444-0000-0000-0000-000000000000"

	repo := remoteRepo(t,
		syncCommit{machine: "Pro16", files: map[string]string{
			"cli/projects/-p/" + keep + ".jsonl":   transcript("still here"),
			"cli/projects/-p/" + pruned + ".jsonl": transcript("aged out later"),
		}},
		// A later sync that removes the aged transcript, as retention does.
		syncCommit{machine: "Pro16", delete: []string{"cli/projects/-p/" + pruned + ".jsonl"}},
	)

	sessions, err := List(ctx, repo, DefaultRef)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want only the surviving one: %+v", len(sessions), sessions)
	}
	if sessions[0].ID != keep {
		t.Fatalf("kept the wrong session: %s", sessions[0].ID)
	}
	// And what is listed can actually be read.
	if _, err := Read(ctx, repo, DefaultRef, sessions[0]); err != nil {
		t.Fatalf("a listed session was not readable: %v", err)
	}
}
