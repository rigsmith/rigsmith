package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/brewrig/inventory"

	// git's post-commit maintenance runs detached and races t.TempDir's
	// cleanup; see the package comment for what that failure looks like.
	_ "github.com/rigsmith/rigsmith/internal/gitquiet"
)

// These run against real git repositories in a temp dir. The store is the only
// part of brewrig that can lose or corrupt another machine's data, and the
// interesting cases — an empty remote, a second machine, a racing push, a
// hostile symlink — are all about git's actual behaviour rather than ours.

// tempDir is t.TempDir with a best-effort removal instead of a fatal one.
//
// t.TempDir fails the test if RemoveAll cannot finish, and a git repository is
// exactly the tree that cannot be relied on to hold still: git spawns detached
// maintenance after a commit, and anything landing under .git/objects between
// RemoveAll reading the directory and unlinking it turns a passed test red.
// internal/gitquiet (imported above) stops the maintenance process being
// spawned and removes most of it; what remains is the filesystem simply not
// having finished, which was still reproducing here about half the time.
//
// These tests assert what git does, not how fast the OS unlinks, so the removal
// is best-effort and the temp root is left behind if it loses the race. Note
// this is the same failure the repo already has a fix in flight for, in
// core/gitrepo and the clauderig suites; this only keeps the new tests honest
// rather than claiming to solve it everywhere.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "brewrig-store-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// bareRemote makes an empty shared repo, the state a freshly created private
// repo is in before anyone syncs.
func bareRemote(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(tempDir(t), "remote.git")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, filepath.Dir(dir), "init", "--bare", "-q", "-b", "main", dir)
	return dir
}

func machine(name string, formulae ...string) *inventory.Machine {
	m := &inventory.Machine{Schema: 1, Name: name, OS: "macos", ChangedAt: time.Now().UTC()}
	for _, f := range formulae {
		m.Formulae = append(m.Formulae, inventory.Package{Name: f})
	}
	m.Normalize()
	return m
}

func openAt(t *testing.T, dir, remote string) *Store {
	t.Helper()
	s, err := Open(context.Background(), dir, remote, "main")
	if err != nil {
		t.Fatal(err)
	}
	// Identity for commits made through the store.
	git(t, dir, "config", "user.name", "t")
	git(t, dir, "config", "user.email", "t@t")
	return s
}

// The first machine to sync meets a repo with no commits and no branch. That
// must not read as a failure, and it must not read as "nothing to publish".
func TestFirstSyncAgainstAnEmptyRemote(t *testing.T) {
	remote := bareRemote(t)
	s := openAt(t, filepath.Join(tempDir(t), "clone"), remote)
	ctx := context.Background()

	if err := s.Pull(ctx); err != nil {
		t.Fatalf("Pull against an empty remote should be a no-op, got %v", err)
	}
	if err := s.Write(machine("pro", "gh")); err != nil {
		t.Fatal(err)
	}
	pushed, err := s.Publish(ctx, "pro: first")
	if err != nil {
		t.Fatal(err)
	}
	if !pushed {
		t.Fatal("Publish reported nothing to push on a first sync")
	}

	all, err := s.Machines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Name != "pro" {
		t.Fatalf("Machines = %v, want just pro", all)
	}
}

// A sync that changed nothing must not commit, or two machines publish
// reordered-but-identical files at each other forever.
func TestPublishingUnchangedInventoryMakesNoCommit(t *testing.T) {
	remote := bareRemote(t)
	s := openAt(t, filepath.Join(tempDir(t), "clone"), remote)
	ctx := context.Background()

	m := machine("pro", "gh", "jq")
	for _, step := range []string{"first", "second"} {
		if err := s.Write(m); err != nil {
			t.Fatal(err)
		}
		pushed, err := s.Publish(ctx, "pro: "+step)
		if err != nil {
			t.Fatal(err)
		}
		if step == "first" && !pushed {
			t.Fatal("the first publish committed nothing")
		}
		if step == "second" && pushed {
			t.Fatal("an unchanged inventory produced a second commit")
		}
	}
}

// Two machines, disjoint files: the second must see the first's inventory.
func TestASecondMachineSeesTheFirst(t *testing.T) {
	remote := bareRemote(t)
	ctx := context.Background()

	a := openAt(t, filepath.Join(tempDir(t), "a"), remote)
	if err := a.Write(machine("air", "ripgrep")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Publish(ctx, "air"); err != nil {
		t.Fatal(err)
	}

	b := openAt(t, filepath.Join(tempDir(t), "b"), remote)
	if err := b.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	all, err := b.Machines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Name != "air" {
		t.Fatalf("second clone sees %v, want air", all)
	}
}

// The race the "disjoint files" argument does NOT cover: both machines commit
// from the same base, so the second push is rejected as non-fast-forward. It
// has to reconcile and land, not leave the clone permanently diverged.
func TestConcurrentPushesFromTheSameBaseBothLand(t *testing.T) {
	remote := bareRemote(t)
	ctx := context.Background()

	a := openAt(t, filepath.Join(tempDir(t), "a"), remote)
	if err := a.Write(machine("air", "ripgrep")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Publish(ctx, "air: first"); err != nil {
		t.Fatal(err)
	}

	// b clones at that point, then a publishes again — b is now behind.
	b := openAt(t, filepath.Join(tempDir(t), "b"), remote)
	if err := b.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Write(machine("air", "ripgrep", "fd")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Publish(ctx, "air: second"); err != nil {
		t.Fatal(err)
	}

	// b publishes its own file from the stale base.
	if err := b.Write(machine("pro", "gh")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Publish(ctx, "pro: first"); err != nil {
		t.Fatalf("a push from a stale base must reconcile and land, got %v", err)
	}

	// Both machines' inventories survive, and b can still pull afterwards.
	if err := b.Pull(ctx); err != nil {
		t.Fatalf("clone left diverged after the racing push: %v", err)
	}
	all, err := b.Machines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, m := range all {
		names[m.Name] = true
	}
	if !names["air"] || !names["pro"] {
		t.Fatalf("machines = %v, want both air and pro", names)
	}
}

// The shared repo is written by other machines. A machine file committed
// elsewhere as a symlink must not be written through.
func TestWriteRefusesToFollowASymlink(t *testing.T) {
	remote := bareRemote(t)
	dir := filepath.Join(tempDir(t), "clone")
	s := openAt(t, dir, remote)

	outside := filepath.Join(tempDir(t), "outside.json")
	if err := os.WriteFile(outside, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "machines"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "machines", "pro.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	err := s.Write(machine("pro", "gh"))
	if err == nil {
		t.Fatal("Write followed a symlink out of the staging repo")
	}
	if !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("error should name the cause, got %v", err)
	}
	got, rerr := os.ReadFile(outside)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(got) != "untouched" {
		t.Fatalf("the file outside the repo was overwritten: %q", got)
	}
}

// The ancestor case, which is the serious one: `machines` itself replaced by a
// link pointing out of the clone. A check-then-open can be raced here by a
// concurrent pull; os.Root resolves descriptor-relative and refuses anything
// that leaves the root, so it cannot be.
func TestWriteRefusesAnAncestorThatEscapesTheClone(t *testing.T) {
	remote := bareRemote(t)
	dir := filepath.Join(tempDir(t), "clone")
	s := openAt(t, dir, remote)

	outside := tempDir(t)
	if err := os.WriteFile(filepath.Join(outside, "pro.json"), []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "machines")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := s.Write(machine("pro", "gh")); err == nil {
		t.Fatal("Write followed a directory symlink out of the staging repo")
	}
	got, rerr := os.ReadFile(filepath.Join(outside, "pro.json"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(got) != "untouched" {
		t.Fatalf("a file outside the repo was overwritten: %q", got)
	}
}

// The inventory is read back by this machine's own next sync, so a partial
// write would block the run that was going to repair it. The write is
// therefore a temp file plus a rename, and neither the temp file nor a
// half-written inventory may be left lying around.
func TestWriteLeavesNoPartialFileBehind(t *testing.T) {
	remote := bareRemote(t)
	dir := tempDir(t)
	s := openAt(t, filepath.Join(dir, "clone"), remote)

	if err := s.Write(machine("pro", "gh")); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(machine("pro", "gh", "jq", "ripgrep")); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "clone", "machines"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != "pro.json" {
		t.Fatalf("machines/ holds %v, want only pro.json — a temp file was left behind", names)
	}

	// And the replacement is whole, not appended to or truncated into.
	all, err := s.Machines(context.Background())
	if err != nil {
		t.Fatalf("the rewritten inventory does not parse: %v", err)
	}
	if len(all) != 1 || len(all[0].Formulae) != 3 {
		t.Fatalf("read back %d machine(s) with %d formulae, want 1 with 3", len(all), len(all[0].Formulae))
	}
}

// A crash between create and rename leaves a temp file behind. Nothing sweeps
// it — a sweep would be the shared-name problem again — so it has to be
// harmless instead: it must not block the next write, must not be read as an
// inventory, and must not reach the shared repo.
func TestATempFileLeftByACrashIsHarmless(t *testing.T) {
	remote := bareRemote(t)
	dir := tempDir(t)
	clone := filepath.Join(dir, "clone")
	s := openAt(t, clone, remote)
	ctx := context.Background()

	if err := s.Write(machine("pro", "gh")); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureReadme(); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(clone, "machines", ".tmpdeadbeefdeadbeef")
	if err := os.WriteFile(stale, []byte("{partial"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.Write(machine("pro", "gh", "jq")); err != nil {
		t.Fatalf("a temp file left by a crash blocked the next write: %v", err)
	}
	if _, err := s.Machines(ctx); err != nil {
		t.Errorf("a stale temp file was read as an inventory: %v", err)
	}

	if _, err := s.Publish(ctx, "pro: after a crash"); err != nil {
		t.Fatal(err)
	}
	tracked := git(t, clone, "ls-files")
	if strings.Contains(tracked, "/.tmp") {
		t.Errorf("a temp file was committed to the shared repo:\n%s", tracked)
	}
}

// Two writers on one machine — a scheduled sync and a manual one — must not
// delete each other's temp file. With a shared temp name the second writer's
// create removes the first's, and one of them renames something that is not
// theirs.
func TestConcurrentWritesOnOneMachineDoNotClash(t *testing.T) {
	remote := bareRemote(t)
	dir := tempDir(t)
	s := openAt(t, filepath.Join(dir, "clone"), remote)

	var wg sync.WaitGroup
	errs := make([]error, 32)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = s.Write(machine("pro", "gh", "jq"))
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent writer %d failed: %v", i, err)
		}
	}
	all, err := s.Machines(context.Background())
	if err != nil {
		t.Fatalf("the inventory does not parse after concurrent writes: %v", err)
	}
	if len(all) != 1 || len(all[0].Formulae) != 2 {
		t.Fatalf("read back %d machine(s), want one with 2 formulae", len(all))
	}
}

// Rename replaces a symlink rather than following it, but the explicit refusal
// stays so the failure is a clear error instead of a quietly deleted link.
// A machine file that will not parse must be reported, not skipped: skipping
// makes that machine's packages look uninstalled everywhere and, worse, makes
// its retirements vanish.
func TestAMalformedMachineFileIsReported(t *testing.T) {
	remote := bareRemote(t)
	dir := filepath.Join(tempDir(t), "clone")
	s := openAt(t, dir, remote)

	if err := os.MkdirAll(filepath.Join(dir, "machines"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "machines", "broken.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := s.Machines(context.Background())
	if err == nil {
		t.Fatal("a malformed machine file was silently skipped")
	}
	if !strings.Contains(err.Error(), "broken.json") {
		t.Errorf("error should name the file, got %v", err)
	}
}

func TestOpenWithoutARemoteIsAnError(t *testing.T) {
	_, err := Open(context.Background(), filepath.Join(tempDir(t), "clone"), "", "main")
	if err == nil {
		t.Fatal("Open succeeded with no remote configured")
	}
}

// Atomic rename stops a half-written file being seen; it does nothing about a
// lost update. Two processes can each snapshot and the one that renames LAST
// wins, even holding older state — and a `retired` or `acknowledged` entry
// written by the loser would be gone for good. That must be refused, not
// performed.
func TestWriteRefusesToReplaceStateItDidNotRead(t *testing.T) {
	remote := bareRemote(t)
	dir := tempDir(t)
	clone := filepath.Join(dir, "clone")
	a := openAt(t, clone, remote)

	if err := a.Write(machine("pro", "gh")); err != nil {
		t.Fatal(err)
	}
	// What this process believes it is replacing.
	if _, _, err := a.Load("pro"); err != nil {
		t.Fatal(err)
	}

	// Another writer replaces the file in between — a second brewrig process
	// on the same machine, which shares the clone but not the Store.
	other := &inventory.Machine{Schema: 1, Name: "pro", OS: "macos"}
	other.Formulae = []inventory.Package{{Name: "somethingelse"}}
	other.Normalize()
	raw, err := inventory.Marshal(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "machines", "pro.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	err = a.Write(machine("pro", "gh", "jq"))
	if err == nil {
		t.Fatal("Write silently replaced an inventory it had never read")
	}
	if !errors.Is(err, ErrStaleBase) {
		t.Errorf("err = %v, want ErrStaleBase so the caller can say what to do", err)
	}

	// And the other writer's state survived intact.
	got, rerr := os.ReadFile(filepath.Join(clone, "machines", "pro.json"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(got) != string(raw) {
		t.Error("the other writer's inventory was overwritten anyway")
	}
	// No temp file left behind by the refusal.
	entries, _ := os.ReadDir(filepath.Join(clone, "machines"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp") {
			t.Errorf("a temp file was left behind after the refusal: %s", e.Name())
		}
	}
}

// The ignore rule has to be a real entry. A comment that merely mentions the
// pattern is not one, and treating it as one leaves temp files stageable —
// which is the whole reason the rule exists.
func TestAMentionOfTheIgnoreRuleIsNotTheRule(t *testing.T) {
	remote := bareRemote(t)
	dir := tempDir(t)
	clone := filepath.Join(dir, "clone")
	s := openAt(t, clone, remote)

	// A .gitignore that talks about the pattern without applying it.
	pre := "# machines/.tmp files are transient\n*.log\n"
	if err := os.WriteFile(filepath.Join(clone, ".gitignore"), []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureReadme(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(clone, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, line := range strings.Split(string(got), "\n") {
		if strings.TrimSpace(line) == "machines/.tmp*" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the ignore rule was not added; a comment was mistaken for it:\n%s", got)
	}
	if !strings.Contains(string(got), "*.log") {
		t.Errorf("the existing .gitignore was clobbered:\n%s", got)
	}

	// And it really does keep a temp file out of a commit.
	if err := s.Write(machine("pro", "gh")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "machines", ".tmpdeadbeef"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(context.Background(), "pro: first"); err != nil {
		t.Fatal(err)
	}
	if tracked := git(t, clone, "ls-files"); strings.Contains(tracked, "/.tmp") {
		t.Errorf("a temp file was committed:\n%s", tracked)
	}
}

// The race the digest alone did not close: separate Stores, DIFFERENT payloads,
// all writing at once. Each write must either land or be refused with
// ErrStaleBase — never report success while quietly discarding another
// writer's inventory — and whatever ends up on disk must be exactly one of the
// payloads, whole.
func TestConcurrentStoresNeverSilentlyLoseAnUpdate(t *testing.T) {
	remote := bareRemote(t)
	dir := tempDir(t)
	clone := filepath.Join(dir, "clone")
	seed := openAt(t, clone, remote)
	if err := seed.Write(machine("pro", "gh")); err != nil {
		t.Fatal(err)
	}

	// Widen the check-to-rename window so the race is actually reachable.
	// Without this the test passes with or without the lock, which would make
	// it evidence for nothing; with it, removing the lock fails the test.
	afterStaleCheck = func() { time.Sleep(20 * time.Millisecond) }
	t.Cleanup(func() { afterStaleCheck = func() {} })

	const writers = 12

	// Every writer opens its own Store and reads the SAME base before any of
	// them writes. Interleaving the reads with the writes — which an earlier
	// version of this test did, by starting each goroutine inside the setup
	// loop — lets later writers legitimately load a base that already includes
	// an earlier write, and they then succeed for entirely correct reasons.
	stores := make([]*Store, writers)
	machines := make([]*inventory.Machine, writers)
	payloads := make([]string, writers)
	for i := 0; i < writers; i++ {
		st, err := Open(context.Background(), clone, remote, "main")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.Load("pro"); err != nil {
			t.Fatal(err)
		}
		m := machine("pro", "gh", fmt.Sprintf("pkg%02d", i))
		raw, err := inventory.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		stores[i], machines[i], payloads[i] = st, m, string(raw)
	}

	var wg sync.WaitGroup
	results := make([]error, writers)
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = stores[i].Write(machines[i])
		}(i)
	}
	close(start)
	wg.Wait()

	landed := 0
	for i, err := range results {
		switch {
		case err == nil:
			landed++
		case errors.Is(err, ErrStaleBase):
			// Correct: refused rather than silently overwriting.
		default:
			t.Errorf("writer %d failed for the wrong reason: %v", i, err)
		}
	}
	// Exactly one. They all loaded the same base, so at most one of them can
	// legitimately replace it; a second success means that writer was told its
	// inventory landed while another overwrote it — the silent lost update.
	if landed != 1 {
		t.Fatalf("%d writers reported success, want exactly 1 — the others were told their "+
			"inventory landed while it was being overwritten", landed)
	}

	// Exactly one payload is on disk, intact — not a blend, not a truncation.
	got, err := os.ReadFile(filepath.Join(clone, "machines", "pro.json"))
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, p := range payloads {
		if string(got) == p {
			matched = true
		}
	}
	if !matched {
		t.Fatalf("what is on disk is not any writer's payload:\n%s", got)
	}
	if _, err := seed.Machines(context.Background()); err != nil {
		t.Errorf("the clone no longer parses: %v", err)
	}
}
