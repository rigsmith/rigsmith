package commitartifact

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func stagedMergeFixture(t *testing.T, format string) (gitRepo, string, string, string) {
	t.Helper()
	r := gitRepo{dir: t.TempDir(), identity: []string{
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.com",
	}}
	mustRun(t, r, "", "init", "--initial-branch=main", "--object-format="+format)
	putPublicationFile(t, r.dir, "state", "base\n")
	mustRun(t, r, "", "add", ".")
	mustRun(t, r, "", "commit", "-m", "base")
	mustRun(t, r, "", "checkout", "-b", "incoming")
	putPublicationFile(t, r.dir, "state", "incoming\n")
	mustRun(t, r, "", "add", ".")
	mustRun(t, r, "", "commit", "-m", "incoming")
	incoming := mustRun(t, r, "", "rev-parse", "HEAD")
	mustRun(t, r, "", "checkout", "main")
	putPublicationFile(t, r.dir, "state", "local\n")
	mustRun(t, r, "", "add", ".")
	mustRun(t, r, "", "commit", "-m", "local")
	original := mustRun(t, r, "", "rev-parse", "HEAD")
	if _, err := r.run(t.Context(), nil, "merge", "--no-commit", "incoming"); !gitExited(err, 1) {
		t.Fatalf("expected conflict: %v", err)
	}
	putPublicationFile(t, r.dir, "state", "resolved\r\n\x00binary\r\n")
	mustRun(t, r, "", "add", "state")
	tree := mustRun(t, r, "", "write-tree")
	return r, original, incoming, tree
}

func mergeFinishPolicy() MergeFinishPolicy {
	return MergeFinishPolicy{Message: "finish saved merge", AuthorName: "fixture", AuthorEmail: "fixture@example.com",
		Time: time.Unix(100, 0), Validate: func(context.Context, string) error { return nil }, Audit: func(context.Context, string) error { return nil }}
}

func readMergeTestFile(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFinishStagedMergePreservesIndexAndWorktree(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			r, original, incoming, tree := stagedMergeFixture(t, format)
			mustRun(t, r, "", "config", "core.fsmonitor", "sh -c 'echo ran > fsmonitor-ran; exit 1' --")
			if format == "sha256" {
				mustRun(t, r, "", "update-index", "--split-index")
			}
			putPublicationFile(t, r.dir, "state", "later unstaged bytes\r\n")
			putPublicationFile(t, r.dir, "untracked", "pending\x00bytes")
			mustRun(t, r, "", "config", "core.hooksPath", filepath.Join(r.dir, "hooks"))
			putPublicationFile(t, r.dir, "hooks/reference-transaction", "#!/bin/sh\necho ran > hook-ran\nexit 99\n")
			if err := os.Chmod(filepath.Join(r.dir, "hooks/reference-transaction"), 0755); err != nil {
				t.Fatal(err)
			}
			before := map[string][]byte{}
			for _, path := range []string{".git/index", ".git/config", "state", "untracked"} {
				before[path] = readMergeTestFile(t, r.dir, path)
			}
			t.Setenv("GIT_DIR", t.TempDir())
			t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "unrelated"))
			calls := 0
			p := mergeFinishPolicy()
			p.Audit = func(context.Context, string) error { calls++; return nil }
			head, err := FinishStagedMerge(t.Context(), r.dir, p)
			if err != nil || head == original || calls != 3 {
				t.Fatalf("finish: %s audits=%d %v", head, calls, err)
			}
			if got := mustRun(t, r, "", "show", "-s", "--format=%T%n%P", head); got != tree+"\n"+original+" "+incoming {
				t.Fatal(got)
			}
			for path, want := range before {
				if !bytes.Equal(readMergeTestFile(t, r.dir, path), want) {
					t.Fatalf("changed %s", path)
				}
			}
			for _, path := range []string{"fsmonitor-ran", "hook-ran"} {
				if _, err := os.Stat(filepath.Join(r.dir, path)); !os.IsNotExist(err) {
					t.Fatal("executed configured hook or filesystem monitor", path, err)
				}
			}
			if settled, err := SettledHead(t.Context(), r.dir); err != nil || settled != head {
				t.Fatalf("not settled: %s %v", settled, err)
			}
			if again, err := FinishStagedMerge(t.Context(), r.dir, p); err != nil || again != head || calls != 3 {
				t.Fatalf("replay: %s %v", again, err)
			}
		})
	}
}

func TestFinishStagedMergeResumesAfterRefUpdate(t *testing.T) {
	for _, changedIndex := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-index", true: "changed-index"}[changedIndex], func(t *testing.T) {
			r, original, incoming, tree := stagedMergeFixture(t, "sha1")
			head := mustRun(t, r, "recovered\n", "commit-tree", tree, "-p", original, "-p", incoming)
			mustRun(t, r, "", "update-ref", "HEAD", head, original)
			if changedIndex {
				putPublicationFile(t, r.dir, "later", "new staged work")
				mustRun(t, r, "", "add", "later")
			}
			before := readMergeTestFile(t, r.dir, ".git/index")
			got, err := FinishStagedMerge(t.Context(), r.dir, mergeFinishPolicy())
			if changedIndex {
				if !errors.Is(err, ErrConflict) {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(r.dir, ".git/MERGE_HEAD")); err != nil {
					t.Fatal("discarded merge", err)
				}
			} else if err != nil || got != head {
				t.Fatalf("resume: %s %v", got, err)
			}
			if !bytes.Equal(before, readMergeTestFile(t, r.dir, ".git/index")) || mustRun(t, r, "", "rev-parse", "HEAD") != head {
				t.Fatal("retry changed saved work or created another commit")
			}
		})
	}
}

func TestFinishStagedMergeRefusesUnsafeState(t *testing.T) {
	for _, kind := range []string{"unresolved", "octopus", "autostash", "rebase", "missing-original", "wrong-original", "audit-parent", "audit-result", "cancel", "changed-index", "changed-head", "limit"} {
		t.Run(kind, func(t *testing.T) {
			r, original, incoming, _ := stagedMergeFixture(t, "sha1")
			p := mergeFinishPolicy()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch kind {
			case "unresolved":
				mustRun(t, r, "", "reset", "--hard", original)
				if _, err := r.run(t.Context(), nil, "merge", "--no-commit", "incoming"); !gitExited(err, 1) {
					t.Fatal(err)
				}
			case "octopus":
				putPublicationFile(t, r.dir, ".git/MERGE_HEAD", incoming+"\n"+original+"\n")
			case "autostash":
				putPublicationFile(t, r.dir, ".git/MERGE_AUTOSTASH", original+"\n")
			case "rebase":
				putPublicationFile(t, r.dir, ".git/rebase-apply", "pending")
			case "missing-original":
				if err := os.Remove(filepath.Join(r.dir, ".git/ORIG_HEAD")); err != nil {
					t.Fatal(err)
				}
			case "wrong-original":
				putPublicationFile(t, r.dir, ".git/ORIG_HEAD", incoming+"\n")
			case "limit":
				p.MaxTreeBytes = 1
			}
			before := readMergeTestFile(t, r.dir, ".git/index")
			merge := readMergeTestFile(t, r.dir, ".git/MERGE_HEAD")
			audits := 0
			p.Audit = func(context.Context, string) error {
				audits++
				if kind == "audit-parent" || (kind == "audit-result" && audits == 3) {
					return errors.New("fixture audit rejection")
				}
				if audits == 3 {
					switch kind {
					case "cancel":
						cancel()
					case "changed-index":
						putPublicationFile(t, r.dir, "pending", "new staged work")
						mustRun(t, r, "", "add", "pending")
						before = readMergeTestFile(t, r.dir, ".git/index")
					case "changed-head":
						mustRun(t, r, "", "update-ref", "HEAD", incoming, original)
						original = incoming
					}
				}
				return nil
			}
			if got, err := FinishStagedMerge(ctx, r.dir, p); err == nil || got != "" {
				t.Fatalf("accepted %s: %s %v", kind, got, err)
			}
			if mustRun(t, r, "", "rev-parse", "HEAD") != original || !bytes.Equal(before, readMergeTestFile(t, r.dir, ".git/index")) || !bytes.Equal(merge, readMergeTestFile(t, r.dir, ".git/MERGE_HEAD")) {
				t.Fatal("failed repair changed canonical state")
			}
		})
	}
}

func TestFinishStagedMergeValidatesPolicyAndMissingStore(t *testing.T) {
	p := mergeFinishPolicy()
	missing := filepath.Join(t.TempDir(), "absent")
	if head, err := FinishStagedMerge(t.Context(), missing, p); err != nil || head != "" {
		t.Fatalf("absent: %s %v", head, err)
	}
	p.AuthorName = "invalid\nname"
	if _, err := FinishStagedMerge(t.Context(), missing, p); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
