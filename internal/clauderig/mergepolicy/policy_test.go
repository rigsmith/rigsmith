package mergepolicy

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
)

// git runs a git command in dir, failing the test on error. Committer dates are
// pinned by the caller through env so the "newest side wins" policy has something
// deterministic to compare — real commits land in the same second.
func git(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func at(stamp string) []string {
	return []string{"GIT_COMMITTER_DATE=" + stamp, "GIT_AUTHOR_DATE=" + stamp}
}

// diverged builds a staging repo where two machines edited the same files from a
// common base, then leaves it mid-merge — the exact state a rejected push puts it
// in, and the state an interrupted sync abandons it in.
func diverged(t *testing.T, base, ours, theirs map[string]string) (string, *gitrepo.Repo) {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	git(t, dir, nil, "init", "-b", "main")
	git(t, dir, nil, "config", "user.email", "t@example.com")
	git(t, dir, nil, "config", "user.name", "t")
	for rel, c := range base {
		write(t, dir, rel, c)
	}
	git(t, dir, at("2026-01-01T00:00:00Z"), "add", "-A")
	git(t, dir, at("2026-01-01T00:00:00Z"), "commit", "-m", "base")

	// the other machine's snapshot, committed LATER
	git(t, dir, nil, "checkout", "-b", "other")
	for rel, c := range theirs {
		write(t, dir, rel, c)
	}
	git(t, dir, at("2026-02-02T00:00:00Z"), "add", "-A")
	git(t, dir, at("2026-02-02T00:00:00Z"), "commit", "-m", "machine B")

	// this machine's snapshot, committed EARLIER
	git(t, dir, nil, "checkout", "main")
	for rel, c := range ours {
		write(t, dir, rel, c)
	}
	git(t, dir, at("2026-01-15T00:00:00Z"), "add", "-A")
	git(t, dir, at("2026-01-15T00:00:00Z"), "commit", "-m", "machine A")

	_ = exec.Command("git", "-C", dir, "merge", "--no-edit", "other").Run() // expected to conflict
	repo, err := gitrepo.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.InMerge(ctx) {
		t.Fatal("expected the repo to be left mid-merge")
	}
	return dir, repo
}

// A memory note two machines each appended to must keep BOTH additions. Taking a
// side here silently deletes something the user wrote on the other computer.
func TestResolve_AppendTextKeepsBothSides(t *testing.T) {
	dir, repo := diverged(t,
		map[string]string{"cli/projects/p/memory/note.md": "# note\nshared line\n"},
		map[string]string{"cli/projects/p/memory/note.md": "# note\nshared line\nlearned on the laptop\n"},
		map[string]string{"cli/projects/p/memory/note.md": "# note\nshared line\nlearned on the desktop\n"},
	)
	rep, err := Resolve(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Unresolved) != 0 {
		t.Fatalf("unresolved: %v", rep.Unresolved)
	}
	got := read(t, dir, "cli/projects/p/memory/note.md")
	for _, want := range []string{"learned on the laptop", "learned on the desktop"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q from the merge:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<<<<<<<") {
		t.Errorf("conflict markers survived into the file:\n%s", got)
	}
	if p := rep.Resolved[0].Policy; p != PolicyUnionText {
		t.Errorf("policy = %q, want %q", p, PolicyUnionText)
	}
}

// Transcripts are append-only, so a session continued on two machines must end up
// holding every line rather than one machine's tail.
func TestResolve_TranscriptKeepsEveryLine(t *testing.T) {
	dir, repo := diverged(t,
		map[string]string{"cli/projects/p/s.jsonl": `{"t":1}` + "\n"},
		map[string]string{"cli/projects/p/s.jsonl": `{"t":1}` + "\n" + `{"t":"laptop"}` + "\n"},
		map[string]string{"cli/projects/p/s.jsonl": `{"t":1}` + "\n" + `{"t":"desktop"}` + "\n"},
	)
	if _, err := Resolve(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	got := read(t, dir, "cli/projects/p/s.jsonl")
	if !strings.Contains(got, "laptop") || !strings.Contains(got, "desktop") {
		t.Errorf("dropped a machine's lines:\n%s", got)
	}
}

// The manifest is what restore reads to map a synced slug back to a directory, so
// a machine missing from it cannot be restored anywhere. Union, never last-writer.
func TestResolve_ManifestUnionsBothMachinesProjects(t *testing.T) {
	mk := func(projects map[string]manifest.Project) string {
		b, _ := json.MarshalIndent(manifest.Manifest{Schema: 1, SourceOS: "macos", Projects: projects}, "", "  ")
		return string(b) + "\n"
	}
	dir, repo := diverged(t,
		map[string]string{manifest.FileName: mk(map[string]manifest.Project{"shared": {Cwd: "/h/shared"}})},
		map[string]string{manifest.FileName: mk(map[string]manifest.Project{"shared": {Cwd: "/h/shared"}, "only-laptop": {Cwd: "/h/a"}})},
		map[string]string{manifest.FileName: mk(map[string]manifest.Project{"shared": {Cwd: "/h/shared"}, "only-desktop": {Cwd: "/h/b"}})},
	)
	rep, err := Resolve(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Unresolved) != 0 {
		t.Fatalf("unresolved: %v", rep.Unresolved)
	}
	var m manifest.Manifest
	if err := json.Unmarshal([]byte(read(t, dir, manifest.FileName)), &m); err != nil {
		t.Fatalf("manifest is not valid JSON after merge: %v", err)
	}
	for _, want := range []string{"shared", "only-laptop", "only-desktop"} {
		if _, ok := m.Projects[want]; !ok {
			t.Errorf("project %q missing from the merged manifest", want)
		}
	}
}

// One machine pushing must not erase another machine's row in the device registry.
func TestResolve_DevicesUnionKeepsEveryMachine(t *testing.T) {
	// Both machines also re-stamp the row they share, which is what makes this a
	// real conflict rather than two additions git can interleave on its own.
	mk := func(sharedVersion string, names ...string) string {
		r := devices.Registry{Schema: 1, Devices: map[string]devices.Device{
			"laptop": {Name: "laptop", OS: "macos", ClaudeVersion: sharedVersion},
		}}
		for _, n := range names {
			r.Devices[n] = devices.Device{Name: n, OS: "macos"}
		}
		b, _ := json.MarshalIndent(r, "", "  ")
		return string(b) + "\n"
	}
	dir, repo := diverged(t,
		map[string]string{devices.FileName: mk("1.0.0")},
		map[string]string{devices.FileName: mk("1.1.0", "laptop2")},
		map[string]string{devices.FileName: mk("1.2.0", "desktop")},
	)
	if _, err := Resolve(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	var r devices.Registry
	if err := json.Unmarshal([]byte(read(t, dir, devices.FileName)), &r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"laptop", "laptop2", "desktop"} {
		if _, ok := r.Devices[want]; !ok {
			t.Errorf("device %q dropped by the merge", want)
		}
	}
}

// Machine-local state (caches, editor bookkeeping) has no meaningful merge, so the
// later snapshot wins — and the result must still be valid JSON, which a line
// union would not be.
func TestResolve_MachineStateTakesNewerSnapshot(t *testing.T) {
	dir, repo := diverged(t,
		map[string]string{"desktop/cache.json": `{"lastUpdated":1}` + "\n"},
		map[string]string{"desktop/cache.json": `{"lastUpdated":2}` + "\n"},
		map[string]string{"desktop/cache.json": `{"lastUpdated":3}` + "\n"},
	)
	rep, err := Resolve(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Unresolved) != 0 {
		t.Fatalf("unresolved: %v", rep.Unresolved)
	}
	var v map[string]int
	got := read(t, dir, "desktop/cache.json")
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Fatalf("merged cache is not valid JSON: %v\n%s", err, got)
	}
	if v["lastUpdated"] != 3 {
		t.Errorf("lastUpdated = %d, want 3 (the later machine's snapshot)", v["lastUpdated"])
	}
	if rep.Resolved[0].Policy != PolicyNewest {
		t.Errorf("policy = %q, want %q", rep.Resolved[0].Policy, PolicyNewest)
	}
}

// Retention on one machine deletes files another machine still has live. That is a
// delete/modify conflict, and the surviving copy is kept: the next sync re-prunes
// if the deletion was right, whereas a wrongly-dropped transcript is unrecoverable.
func TestResolve_DeleteVersusEditKeepsTheSurvivor(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	git(t, dir, nil, "init", "-b", "main")
	git(t, dir, nil, "config", "user.email", "t@example.com")
	git(t, dir, nil, "config", "user.name", "t")
	write(t, dir, "cli/projects/p/old.jsonl", `{"t":1}`+"\n")
	git(t, dir, at("2026-01-01T00:00:00Z"), "add", "-A")
	git(t, dir, at("2026-01-01T00:00:00Z"), "commit", "-m", "base")

	git(t, dir, nil, "checkout", "-b", "other")
	if err := os.Remove(filepath.Join(dir, "cli/projects/p/old.jsonl")); err != nil {
		t.Fatal(err)
	}
	git(t, dir, at("2026-02-02T00:00:00Z"), "add", "-A")
	git(t, dir, at("2026-02-02T00:00:00Z"), "commit", "-m", "pruned by retention")

	git(t, dir, nil, "checkout", "main")
	write(t, dir, "cli/projects/p/old.jsonl", `{"t":1}`+"\n"+`{"t":"still active here"}`+"\n")
	git(t, dir, at("2026-01-15T00:00:00Z"), "add", "-A")
	git(t, dir, at("2026-01-15T00:00:00Z"), "commit", "-m", "still in use")

	_ = exec.Command("git", "-C", dir, "merge", "--no-edit", "other").Run()
	repo, err := gitrepo.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Resolve(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Unresolved) != 0 {
		t.Fatalf("unresolved: %v", rep.Unresolved)
	}
	if got := read(t, dir, "cli/projects/p/old.jsonl"); !strings.Contains(got, "still active here") {
		t.Errorf("the surviving copy was dropped:\n%s", got)
	}
}

// Resolve must leave the merge committable — the whole point is that no human is
// there to finish it.
func TestResolve_LeavesMergeReadyToCommit(t *testing.T) {
	_, repo := diverged(t,
		map[string]string{"a.md": "base\n", "desktop/c.json": `{"v":1}` + "\n"},
		map[string]string{"a.md": "base\nlaptop\n", "desktop/c.json": `{"v":2}` + "\n"},
		map[string]string{"a.md": "base\ndesktop\n", "desktop/c.json": `{"v":3}` + "\n"},
	)
	ctx := context.Background()
	if _, err := Resolve(ctx, repo); err != nil {
		t.Fatal(err)
	}
	left, err := repo.Conflicts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("still conflicted: %v", left)
	}
	if err := repo.CommitMerge(ctx); err != nil {
		t.Fatalf("merge could not be committed: %v", err)
	}
	if repo.InMerge(ctx) {
		t.Error("repo is still mid-merge after committing")
	}
}

// Union keeps both machines' work, and git's own 3-way merge already emits an
// addition both sides made only ONCE — the dedup below is for what it cannot
// align, not for the ordinary case. Pinned because it is the premise of that
// dedup: if this ever changed, every merged transcript would double.
func TestResolve_IdenticalAdditionsAreNotDuplicated(t *testing.T) {
	rel := "cli/memory/notes.md"
	base := "# notes\n\n- one\n"
	dir, repo := diverged(t,
		map[string]string{rel: base},
		map[string]string{rel: base + "\n- shared\n- from A\n"},
		map[string]string{rel: base + "\n- shared\n- from B\n"})

	if _, err := Resolve(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	got := read(t, dir, rel)
	if n := strings.Count(got, "- shared"); n != 1 {
		t.Errorf("an addition both machines made should appear once, got %d:\n%s", n, got)
	}
	for _, want := range []string{"- from A", "- from B"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s:\n%s", want, got)
		}
	}
}

// What git cannot align, it duplicates. Here both machines recorded the same
// turn but with different surrounding records, so the union emits it twice —
// and in a transcript that is not a cosmetic repeat: records carry
// uuid/parentUuid links and `claude --resume` walks them in order, so a repeated
// uuid is a fork in the chain.
func TestResolve_TranscriptDropsRecordsUnionCouldNotAlign(t *testing.T) {
	rel := "cli/projects/-p/s.jsonl"
	rec := func(id string) string { return `{"uuid":"` + id + `","type":"user"}` + "\n" }
	base := rec("a")
	// The shared record lands in a different position on each side, so diff3 has
	// nothing to pair it against.
	ours := base + rec("shared") + rec("ours-1")
	theirs := base + rec("theirs-1") + rec("theirs-2") + rec("shared")

	dir, repo := diverged(t,
		map[string]string{rel: base},
		map[string]string{rel: ours},
		map[string]string{rel: theirs})

	rep, err := Resolve(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Unresolved) != 0 {
		t.Fatalf("unresolved: %v", rep.Unresolved)
	}
	got := read(t, dir, rel)
	// Nothing either machine recorded may be lost…
	for _, want := range []string{`"ours-1"`, `"theirs-1"`, `"theirs-2"`, `"shared"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s:\n%s", want, got)
		}
	}
	// …and the record both have appears exactly once.
	if n := strings.Count(got, `"uuid":"shared"`); n != 1 {
		t.Errorf("repeated record kept %d times, want 1:\n%s", n, got)
	}
	var note string
	for _, r := range rep.Resolved {
		if r.Path == rel {
			note = r.Note
		}
	}
	if !strings.Contains(note, "repeated record") {
		t.Errorf("a silent edit to a transcript is the thing nobody should discover later; note = %q", note)
	}
}

// The survivor of a delete-vs-edit is reported as "kept", not "newest": no
// comparison happened, one side simply still exists.
func TestResolve_DeleteVersusEditIsReportedAsKept(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	git(t, dir, nil, "init", "-b", "main")
	git(t, dir, nil, "config", "user.email", "t@example.com")
	git(t, dir, nil, "config", "user.name", "t")
	write(t, dir, "cli/statsig/cache.json", `{"v":1}`+"\n")
	git(t, dir, at("2026-01-01T00:00:00Z"), "add", "-A")
	git(t, dir, at("2026-01-01T00:00:00Z"), "commit", "-m", "base")

	git(t, dir, nil, "checkout", "-b", "other")
	if err := os.Remove(filepath.Join(dir, "cli/statsig/cache.json")); err != nil {
		t.Fatal(err)
	}
	git(t, dir, at("2026-02-02T00:00:00Z"), "add", "-A")
	git(t, dir, at("2026-02-02T00:00:00Z"), "commit", "-m", "gone there")

	git(t, dir, nil, "checkout", "main")
	write(t, dir, "cli/statsig/cache.json", `{"v":2}`+"\n")
	git(t, dir, at("2026-01-15T00:00:00Z"), "add", "-A")
	git(t, dir, at("2026-01-15T00:00:00Z"), "commit", "-m", "edited here")

	_ = exec.Command("git", "-C", dir, "merge", "--no-edit", "other").Run()
	repo, err := gitrepo.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Resolve(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rep.Resolved {
		if r.Path == "cli/statsig/cache.json" {
			found = true
			if r.Policy != PolicyKeep {
				t.Errorf("policy = %q, want %q (note: %s)", r.Policy, PolicyKeep, r.Note)
			}
		}
	}
	if !found {
		t.Fatalf("path not resolved: %+v", rep)
	}
}

// Prose is unioned only inside memory/. Everything else the sync carries —
// CLAUDE.md, skills, plans, commands — is a document someone EDITS, and
// concatenating two revisions of one would produce a file that contradicts
// itself while the report claims a clean merge.
func TestResolve_EditedDocumentsTakeTheNewerSnapshotNotBothSides(t *testing.T) {
	for _, rel := range []string{"cli/CLAUDE.md", "cli/skills/deploy/SKILL.md", "cli/plans/roadmap.md"} {
		t.Run(rel, func(t *testing.T) {
			dir, repo := diverged(t,
				map[string]string{rel: "# doc\nalways use pnpm\n"},
				map[string]string{rel: "# doc\nalways use pnpm\nprefer tabs\n"},
				map[string]string{rel: "# doc\nalways use pnpm\nprefer spaces\n"})

			rep, err := Resolve(context.Background(), repo)
			if err != nil {
				t.Fatal(err)
			}
			if len(rep.Unresolved) != 0 {
				t.Fatalf("unresolved: %v", rep.Unresolved)
			}
			got := read(t, dir, rel)
			// The other machine's snapshot is the newer one in this fixture.
			if !strings.Contains(got, "prefer spaces") {
				t.Errorf("newer snapshot lost:\n%s", got)
			}
			if strings.Contains(got, "prefer tabs") {
				t.Errorf("two revisions were concatenated into one contradictory doc:\n%s", got)
			}
			if p := rep.Resolved[0].Policy; p != PolicyNewest {
				t.Errorf("policy = %q, want %q", p, PolicyNewest)
			}
		})
	}
}

// A memory note under a project keeps both sides, which is the case the union
// exists for — pinned next to the negative above so the boundary is visible.
func TestResolve_MemoryNotesStillUnion(t *testing.T) {
	rel := "cli/projects/-p/memory/MEMORY.md"
	dir, repo := diverged(t,
		map[string]string{rel: "# index\n- one\n"},
		map[string]string{rel: "# index\n- one\n- from the laptop\n"},
		map[string]string{rel: "# index\n- one\n- from the desktop\n"})

	if _, err := Resolve(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	got := read(t, dir, rel)
	for _, want := range []string{"from the laptop", "from the desktop"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q:\n%s", want, got)
		}
	}
}

// git QUOTES paths with non-ASCII characters unless asked for NUL-delimited
// output, and a quoted display string is not a path any resolver can open — so
// the conflicts hardest to fix by hand would be the ones nothing could fix at
// all. Project slugs come from directory names, so this is ordinary, not exotic.
func TestResolve_HandlesNonASCIIPaths(t *testing.T) {
	rel := "cli/projects/-Users-j-Café/memory/notes.md"
	dir, repo := diverged(t,
		map[string]string{rel: "# café\n- one\n"},
		map[string]string{rel: "# café\n- one\n- from the laptop\n"},
		map[string]string{rel: "# café\n- one\n- from the desktop\n"})

	rep, err := Resolve(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Unresolved) != 0 {
		t.Fatalf("a non-ASCII path was left unresolved: %v", rep.Unresolved)
	}
	got := read(t, dir, rel)
	if strings.Contains(got, "<<<<<<<") {
		t.Errorf("conflict markers survived:\n%s", got)
	}
	for _, want := range []string{"from the laptop", "from the desktop"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q:\n%s", want, got)
		}
	}
}
