package commitartifact

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMergeStageCompleteAndReplay(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			r, original, incoming := unresolvedMergeFixture(t, format)
			if format == "sha256" {
				// Detach without refreshing the unresolved index/worktree.
				mustRun(t, r, "", "update-ref", "--no-deref", "HEAD", original)
				mustRun(t, r, "", "update-index", "--split-index")
				mustRun(t, r, "", "config", "i18n.commitEncoding", "ISO-8859-1")
			}
			putPublicationFile(t, r.dir, "pending", "pending bytes\n")
			putPublicationFile(t, r.dir, "clean", "later unrelated edit\n")
			store := MergeStageStore{Dir: filepath.Join(t.TempDir(), "intent")}
			p := stagePolicy(t)
			head, err := store.Complete(t.Context(), r.dir, p)
			if err != nil {
				t.Fatal(err)
			}
			if got := mustRun(t, r, "", "show", "-s", "--format=%P", head); got != original+" "+incoming {
				t.Fatal("wrong completed parents", got)
			}
			index := readMergeTestFile(t, r.dir, ".git/index")
			p.Resolve = func(context.Context, string, []byte, []byte, []byte, RelatedFiles) ([]byte, error) {
				t.Fatal("replanned completed merge")
				return nil, nil
			}
			if again, err := store.Complete(t.Context(), r.dir, p); err != nil || again != head {
				t.Fatal("completed replay", again, err)
			}
			if !bytes.Equal(index, readMergeTestFile(t, r.dir, ".git/index")) {
				t.Fatal("replay changed index")
			}
			for path, want := range map[string]string{"state": "both\r\n\x00raw bytes\n", "parts/new": "retained companion\n", "pending": "pending bytes\n", "clean": "later unrelated edit\n"} {
				if string(readMergeTestFile(t, r.dir, path)) != want {
					t.Fatal("changed bytes", path)
				}
			}
			if settled, err := SettledHead(t.Context(), r.dir); err != nil || settled != head {
				t.Fatal("not settled", settled, err)
			}
		})
	}
}

func TestMergeStageCompleteProcessCrash(t *testing.T) {
	if root := os.Getenv("RIG_COMPLETE_CRASH_ROOT"); root != "" {
		store := MergeStageStore{Dir: os.Getenv("RIG_COMPLETE_CRASH_STORE"), afterWrite: func(point string) error {
			if point == os.Getenv("RIG_COMPLETE_CRASH_POINT") {
				os.Exit(73)
			}
			return nil
		}}
		if _, err := store.Complete(t.Context(), root, stagePolicy(t)); err != nil {
			t.Fatal(err)
		}
		t.Fatal("missed crash point")
	}
	for _, point := range []string{"parts/new", "index", "head", "complete"} {
		t.Run(point, func(t *testing.T) {
			r, _, _ := unresolvedMergeFixture(t, "sha1")
			store := MergeStageStore{Dir: filepath.Join(t.TempDir(), "intent")}
			child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMergeStageCompleteProcessCrash$")
			child.Env = append(os.Environ(), "RIG_COMPLETE_CRASH_ROOT="+r.dir, "RIG_COMPLETE_CRASH_STORE="+store.Dir, "RIG_COMPLETE_CRASH_POINT="+point)
			out, err := child.CombinedOutput()
			var exited *exec.ExitError
			if !errors.As(err, &exited) || exited.ExitCode() != 73 {
				t.Fatalf("expected process crash: %v %s", err, out)
			}
			p := stagePolicy(t)
			p.Resolve = func(context.Context, string, []byte, []byte, []byte, RelatedFiles) ([]byte, error) {
				t.Fatal("replanned after crash")
				return nil, nil
			}
			head, err := store.Complete(t.Context(), r.dir, p)
			if err != nil {
				t.Fatal("restart", err)
			}
			if settled, err := SettledHead(t.Context(), r.dir); err != nil || settled != head {
				t.Fatal("not settled", settled, err)
			}
			if _, err := os.Stat(filepath.Join(r.dir, ".git/index.lock")); !os.IsNotExist(err) {
				t.Fatal("left owned lock", err)
			}
		})
	}
}

func TestMergeStageCompleteRefusesChangedState(t *testing.T) {
	for _, change := range []string{"file", "index", "branch", "orig", "merge", "operation", "policy"} {
		t.Run(change, func(t *testing.T) {
			r, _, incoming := unresolvedMergeFixture(t, "sha1")
			store := MergeStageStore{Dir: filepath.Join(t.TempDir(), "intent")}
			p := stagePolicy(t)
			head, err := store.Complete(t.Context(), r.dir, p)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "file":
				putPublicationFile(t, r.dir, "state", "later affected edit")
			case "index":
				putPublicationFile(t, r.dir, "extra", "later staged edit")
				mustRun(t, r, "", "add", "extra")
			case "branch":
				mustRun(t, r, "", "update-ref", "refs/heads/other", head)
				mustRun(t, r, "", "symbolic-ref", "HEAD", "refs/heads/other")
			case "orig":
				putPublicationFile(t, r.dir, ".git/ORIG_HEAD", incoming+"\n")
			case "merge":
				putPublicationFile(t, r.dir, ".git/MERGE_HEAD", head+"\n")
			case "operation":
				putPublicationFile(t, r.dir, ".git/CHERRY_PICK_HEAD", incoming+"\n")
			case "policy":
				p.Audit = func(context.Context, string) error { return errors.New("stricter current audit") }
			}
			index := readMergeTestFile(t, r.dir, ".git/index")
			live := readMergeTestFile(t, r.dir, "state")
			if _, err := store.Complete(t.Context(), r.dir, p); err == nil {
				t.Fatal("accepted changed recovery state")
			}
			if !bytes.Equal(index, readMergeTestFile(t, r.dir, ".git/index")) || !bytes.Equal(live, readMergeTestFile(t, r.dir, "state")) {
				t.Fatal("refusal modified index or file")
			}
		})
	}
}

func TestMergeStageCompletePartialMetadataCleanup(t *testing.T) {
	r, _, _ := unresolvedMergeFixture(t, "sha1")
	store := MergeStageStore{Dir: filepath.Join(t.TempDir(), "intent"), afterWrite: func(point string) error {
		if point == "head" {
			return errors.New("interrupted cleanup")
		}
		return nil
	}}
	if _, err := store.Complete(t.Context(), r.dir, stagePolicy(t)); err == nil {
		t.Fatal("missed interruption")
	}
	if err := os.Remove(filepath.Join(r.dir, ".git/MERGE_HEAD")); err != nil {
		t.Fatal(err)
	}
	store.afterWrite = nil
	if _, err := store.Complete(t.Context(), r.dir, stagePolicy(t)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"MERGE_HEAD", "MERGE_MSG", "AUTO_MERGE"} {
		if _, err := os.Stat(filepath.Join(r.dir, ".git", path)); !os.IsNotExist(err) {
			t.Fatal("left merge metadata", path, err)
		}
	}
}
