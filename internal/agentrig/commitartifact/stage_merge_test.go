package commitartifact

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func stagePolicy(t *testing.T) MergeStagePolicy {
	p := mergePlanPolicy(t)
	resolve := p.Resolve
	p.Resolve = func(ctx context.Context, path string, base, ours, theirs []byte, files RelatedFiles) ([]byte, error) {
		if err := files.Add(ctx, "parts/new", []byte("retained companion\n")); err != nil {
			return nil, err
		}
		return resolve(ctx, path, base, ours, theirs, files)
	}
	return MergeStagePolicy{MergePlanPolicy: p, PolicyID: "fixture-stage-v1"}
}

func TestMergeStagePreservesPendingEditsAndCompletes(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			r, original, incoming := unresolvedMergeFixture(t, format)
			if format == "sha256" {
				mustRun(t, r, "", "update-index", "--split-index")
			}
			putPublicationFile(t, r.dir, "clean", "later unstaged clean edit\n")
			putPublicationFile(t, r.dir, "pending", "untracked pending\n")
			before := map[string][]byte{}
			for _, path := range []string{".git/HEAD", ".git/MERGE_HEAD", ".git/ORIG_HEAD", ".git/MERGE_MSG", ".git/AUTO_MERGE", "clean", "pending"} {
				before[path] = readMergeTestFile(t, r.dir, path)
			}
			store := MergeStageStore{Dir: filepath.Join(t.TempDir(), "intent")}
			p := stagePolicy(t)
			tree, err := store.Stage(t.Context(), r.dir, p)
			if err != nil {
				t.Fatal(err)
			}
			for path, data := range before {
				if !bytes.Equal(data, readMergeTestFile(t, r.dir, path)) {
					t.Fatal("changed pending state", path)
				}
			}
			if got := mustRun(t, r, "", "write-tree"); got != tree {
				t.Fatal("wrong staged tree", got, tree)
			}
			if got := string(readMergeTestFile(t, r.dir, "state")); got != "both\r\n\x00raw bytes\n" {
				t.Fatalf("changed result bytes %q", got)
			}
			if got := string(readMergeTestFile(t, r.dir, "parts/new")); got != "retained companion\n" {
				t.Fatal("missing companion", got)
			}
			if _, err := os.Stat(filepath.Join(r.dir, ".git/index.lock")); !os.IsNotExist(err) {
				t.Fatal("left index lock", err)
			}
			// A sealed intent supports retry after final index installation, without
			// trying to plan again from an index that no longer has unresolved entries.
			p.Resolve = func(context.Context, string, []byte, []byte, []byte, RelatedFiles) ([]byte, error) {
				t.Fatal("replanned sealed repair")
				return nil, nil
			}
			if again, err := store.Stage(t.Context(), r.dir, p); err != nil || again != tree {
				t.Fatal("retry", again, err)
			}
			head, err := FinishStagedMerge(t.Context(), r.dir, p.MergeFinishPolicy)
			if err != nil {
				t.Fatal(err)
			}
			if got := mustRun(t, r, "", "show", "-s", "--format=%T%n%P", head); got != tree+"\n"+original+" "+incoming {
				t.Fatal("wrong completed ancestry", got)
			}
			if string(readMergeTestFile(t, r.dir, "clean")) != "later unstaged clean edit\n" {
				t.Fatal("lost unrelated edit")
			}
			if _, err := store.Stage(t.Context(), r.dir, p); err == nil {
				t.Fatal("reapplied completed merge")
			}
		})
	}
}

func TestMergeStageResumesInterruptedWrites(t *testing.T) {
	for _, stop := range []string{"parts/new", "state", "index"} {
		t.Run(stop, func(t *testing.T) {
			r, original, _ := unresolvedMergeFixture(t, "sha1")
			before := readMergeTestFile(t, r.dir, ".git/index")
			stopped := errors.New("synthetic interruption")
			store := MergeStageStore{Dir: filepath.Join(t.TempDir(), "intent"), afterWrite: func(path string) error {
				if path == stop {
					return stopped
				}
				return nil
			}}
			p := stagePolicy(t)
			if _, err := store.Stage(t.Context(), r.dir, p); !errors.Is(err, stopped) {
				t.Fatal(err)
			}
			if mustRun(t, r, "", "rev-parse", "HEAD") != original {
				t.Fatal("moved HEAD")
			}
			if stop != "index" && !bytes.Equal(before, readMergeTestFile(t, r.dir, ".git/index")) {
				t.Fatal("installed index before all files")
			}
			store.afterWrite = nil
			p.Resolve = func(context.Context, string, []byte, []byte, []byte, RelatedFiles) ([]byte, error) {
				t.Fatal("replanned after interruption")
				return nil, nil
			}
			if _, err := store.Stage(t.Context(), r.dir, p); err != nil {
				t.Fatal(err)
			}
			if mustRun(t, r, "", "ls-files", "--unmerged") != "" {
				t.Fatal("unresolved index after retry")
			}
			if string(readMergeTestFile(t, r.dir, "parts/new")) != "retained companion\n" {
				t.Fatal("lost companion")
			}
		})
	}
}

func TestMergeStageRefusesLaterEditsOnRetry(t *testing.T) {
	for _, kind := range []string{"installed-file", "pending-file", "index", "auto-merge", "head", "new-operation"} {
		t.Run(kind, func(t *testing.T) {
			r, _, incoming := unresolvedMergeFixture(t, "sha1")
			store := MergeStageStore{Dir: filepath.Join(t.TempDir(), "intent"), afterWrite: func(string) error { return errors.New("stop") }}
			p := stagePolicy(t)
			if _, err := store.Stage(t.Context(), r.dir, p); err == nil {
				t.Fatal("expected interruption")
			}
			switch kind {
			case "installed-file":
				putPublicationFile(t, r.dir, "parts/new", "later installed-file edit")
			case "pending-file":
				putPublicationFile(t, r.dir, "state", "later conflict edit")
			case "index":
				putPublicationFile(t, r.dir, "extra", "later staged edit")
				mustRun(t, r, "", "add", "extra")
			case "auto-merge":
				mustRun(t, r, "", "update-ref", "AUTO_MERGE", mustRun(t, r, "", "rev-parse", incoming+"^{tree}"))
			case "head":
				mustRun(t, r, "", "update-ref", "HEAD", incoming)
			case "new-operation":
				putPublicationFile(t, r.dir, ".git/BISECT_START", "main\n")
			}
			index := readMergeTestFile(t, r.dir, ".git/index")
			state := readMergeTestFile(t, r.dir, "state")
			part := readMergeTestFile(t, r.dir, "parts/new")
			store.afterWrite = nil
			if _, err := store.Stage(t.Context(), r.dir, p); err == nil {
				t.Fatal("accepted later edit")
			}
			if !bytes.Equal(index, readMergeTestFile(t, r.dir, ".git/index")) || !bytes.Equal(state, readMergeTestFile(t, r.dir, "state")) || !bytes.Equal(part, readMergeTestFile(t, r.dir, "parts/new")) {
				t.Fatal("changed later edit on refusal")
			}
		})
	}
}

func TestMergeStagePreflightRefusals(t *testing.T) {
	for _, kind := range []string{"edited-conflict", "untracked-collision", "symlink-parent", "missing-auto", "assume-unchanged", "skip-worktree", "index-lock", "audit", "capacity", "foreign-store"} {
		t.Run(kind, func(t *testing.T) {
			r, original, _ := unresolvedMergeFixture(t, "sha1")
			store := MergeStageStore{Dir: filepath.Join(t.TempDir(), "intent")}
			p := stagePolicy(t)
			switch kind {
			case "edited-conflict":
				putPublicationFile(t, r.dir, "state", "pending manual resolution")
			case "untracked-collision":
				putPublicationFile(t, r.dir, "parts/new", "retained companion\n")
			case "symlink-parent":
				if err := os.Symlink(t.TempDir(), filepath.Join(r.dir, "parts")); err != nil {
					t.Skip("symlink unavailable", err)
				}
			case "missing-auto":
				mustRun(t, r, "", "update-ref", "-d", "AUTO_MERGE")
			case "assume-unchanged":
				mustRun(t, r, "", "update-index", "--assume-unchanged", "clean")
			case "skip-worktree":
				mustRun(t, r, "", "update-index", "--skip-worktree", "clean")
			case "index-lock":
				putPublicationFile(t, r.dir, ".git/index.lock", "someone else owns this")
			case "audit":
				p.Audit = func(context.Context, string) error { return errors.New("synthetic scan failure") }
			case "capacity":
				store.MaxBytes = 100
			case "foreign-store":
				store.Dir = filepath.Join(r.dir, "intent")
			}
			before := readMergeTestFile(t, r.dir, ".git/index")
			state := readMergeTestFile(t, r.dir, "state")
			if _, err := store.Stage(t.Context(), r.dir, p); err == nil {
				t.Fatal("accepted", kind)
			}
			if !bytes.Equal(before, readMergeTestFile(t, r.dir, ".git/index")) || !bytes.Equal(state, readMergeTestFile(t, r.dir, "state")) || mustRun(t, r, "", "rev-parse", "HEAD") != original {
				t.Fatal("modified canonical state on refusal")
			}
			if kind == "index-lock" && string(readMergeTestFile(t, r.dir, ".git/index.lock")) != "someone else owns this" {
				t.Fatal("removed foreign lock")
			}
		})
	}
}

func TestMergeStageOwnedIndexLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.lock")
	ref := strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
	release, err := mergeStageIndexLock(path, ref)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a process exit that left the recognizable lock behind.
	resumed, err := mergeStageIndexLock(path, ref)
	if err != nil {
		t.Fatal(err)
	}
	resumed()
	release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("owned lock retained", err)
	}
}

func TestMergeStageProcessCrash(t *testing.T) {
	if root := os.Getenv("RIG_STAGE_CRASH_ROOT"); root != "" {
		store := MergeStageStore{Dir: os.Getenv("RIG_STAGE_CRASH_STORE"), afterWrite: func(path string) error {
			if path == os.Getenv("RIG_STAGE_CRASH_STOP") {
				os.Exit(73)
			}
			return nil
		}}
		if _, err := store.Stage(t.Context(), root, stagePolicy(t)); err != nil {
			t.Fatal(err)
		}
		t.Fatal("did not reach crash point")
	}
	for _, stop := range []string{"parts/new", "index"} {
		t.Run(stop, func(t *testing.T) {
			r, _, _ := unresolvedMergeFixture(t, "sha1")
			store := MergeStageStore{Dir: filepath.Join(t.TempDir(), "intent")}
			child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMergeStageProcessCrash$")
			child.Env = append(os.Environ(), "RIG_STAGE_CRASH_ROOT="+r.dir, "RIG_STAGE_CRASH_STORE="+store.Dir, "RIG_STAGE_CRASH_STOP="+stop)
			out, err := child.CombinedOutput()
			var exited *exec.ExitError
			if !errors.As(err, &exited) || exited.ExitCode() != 73 {
				t.Fatalf("child did not crash at checkpoint: %v %s", err, out)
			}
			if _, err := os.Stat(filepath.Join(r.dir, ".git/index.lock")); err != nil {
				t.Fatal("expected retained owned lock", err)
			}
			if _, err := store.Stage(t.Context(), r.dir, stagePolicy(t)); err != nil {
				t.Fatal("restart recovery", err)
			}
			if got := mustRun(t, r, "", "ls-files", "--unmerged"); got != "" {
				t.Fatal("unresolved after process restart", got)
			}
			if _, err := os.Stat(filepath.Join(r.dir, ".git/index.lock")); !os.IsNotExist(err) {
				t.Fatal("left owned lock", err)
			}
		})
	}
}

func TestMergeStageLinkedWorktree(t *testing.T) {
	r, original, _ := unresolvedMergeFixture(t, "sha1")
	linked := filepath.Join(t.TempDir(), "linked")
	mustRun(t, r, "", "worktree", "add", "--detach", linked, original)
	source := r
	source.dir = linked
	if _, err := source.run(t.Context(), nil, "merge", "--no-commit", "incoming"); !gitExited(err, 1) {
		t.Fatal(err)
	}
	primary := readMergeTestFile(t, r.dir, ".git/index")
	store := MergeStageStore{Dir: filepath.Join(t.TempDir(), "intent")}
	if _, err := store.Stage(t.Context(), linked, stagePolicy(t)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(primary, readMergeTestFile(t, r.dir, ".git/index")) {
		t.Fatal("modified primary worktree index")
	}
	if got := mustRun(t, source, "", "ls-files", "--unmerged"); got != "" {
		t.Fatal("linked index unresolved")
	}
}

func TestMergeStageCancelledRepairAndCorruptIntent(t *testing.T) {
	for _, kind := range []string{"cancel", "corrupt"} {
		t.Run(kind, func(t *testing.T) {
			r, _, _ := unresolvedMergeFixture(t, "sha1")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store := MergeStageStore{Dir: filepath.Join(t.TempDir(), "intent"), afterWrite: func(string) error { cancel(); return context.Canceled }}
			if _, err := store.Stage(ctx, r.dir, stagePolicy(t)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			store.afterWrite = nil
			if kind == "cancel" {
				if _, err := store.Stage(t.Context(), r.dir, stagePolicy(t)); err != nil {
					t.Fatal("cancelled repair did not resume", err)
				}
				return
			}
			archives, err := filepath.Glob(filepath.Join(store.Dir, "*.capture"))
			if err != nil || len(archives) != 1 {
				t.Fatal("missing sealed intent", archives, err)
			}
			f, err := os.OpenFile(archives[0], os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteAt([]byte("corrupt"), 0)
			closeErr := f.Close()
			if err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
			index := readMergeTestFile(t, r.dir, ".git/index")
			state := readMergeTestFile(t, r.dir, "state")
			part := readMergeTestFile(t, r.dir, "parts/new")
			if _, err := store.Stage(t.Context(), r.dir, stagePolicy(t)); err == nil {
				t.Fatal("accepted corrupt intent")
			}
			if !bytes.Equal(index, readMergeTestFile(t, r.dir, ".git/index")) || !bytes.Equal(state, readMergeTestFile(t, r.dir, "state")) || !bytes.Equal(part, readMergeTestFile(t, r.dir, "parts/new")) {
				t.Fatal("changed canonical state after corrupt intent")
			}
		})
	}
}

func TestMergeStageRetryRevalidatesPolicy(t *testing.T) {
	for _, kind := range []string{"bundle-limit", "tree-limit", "store-limit", "audit"} {
		t.Run(kind, func(t *testing.T) {
			r, _, _ := unresolvedMergeFixture(t, "sha1")
			store := MergeStageStore{Dir: filepath.Join(t.TempDir(), "intent"), afterWrite: func(string) error { return errors.New("stop") }}
			p := stagePolicy(t)
			if _, err := store.Stage(t.Context(), r.dir, p); err == nil {
				t.Fatal("expected interruption")
			}
			store.afterWrite = nil
			switch kind {
			case "bundle-limit":
				p.MaxBundleBytes = 1
			case "tree-limit":
				p.MaxTreeBytes = 1
			case "store-limit":
				store.MaxBytes = 100
			case "audit":
				p.Audit = func(context.Context, string) error { return errors.New("scan rejected retry") }
			}
			index := readMergeTestFile(t, r.dir, ".git/index")
			state := readMergeTestFile(t, r.dir, "state")
			if _, err := store.Stage(t.Context(), r.dir, p); err == nil {
				t.Fatal("ignored current retry policy")
			}
			if !bytes.Equal(index, readMergeTestFile(t, r.dir, ".git/index")) || !bytes.Equal(state, readMergeTestFile(t, r.dir, "state")) {
				t.Fatal("wrote before retry policy checks")
			}
		})
	}
}
