package commitartifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

func unresolvedMergeFixture(t *testing.T, format string) (gitRepo, string, string) {
	t.Helper()
	r, original, incoming, _ := stagedMergeFixture(t, format)
	mustRun(t, r, "", "reset", "--hard", original)
	putPublicationFile(t, r.dir, "clean", "nonconflicting local bytes\n")
	mustRun(t, r, "", "add", "clean")
	mustRun(t, r, "", "commit", "-m", "nonconflicting file")
	original = mustRun(t, r, "", "rev-parse", "HEAD")
	if _, err := r.run(t.Context(), nil, "merge", "--no-commit", "incoming"); !gitExited(err, 1) {
		t.Fatalf("expected unresolved merge: %v", err)
	}
	return r, original, incoming
}

func mergePlanPolicy(t *testing.T) MergePlanPolicy {
	t.Helper()
	return MergePlanPolicy{MergeFinishPolicy: mergeFinishPolicy(), Resolve: func(_ context.Context, path string, base, ours, theirs []byte, _ RelatedFiles) ([]byte, error) {
		if path != "state" || string(base) != "base\n" || string(ours) != "local\n" || string(theirs) != "incoming\n" {
			t.Fatalf("wrong conflict input: %s %q %q %q", path, base, ours, theirs)
		}
		return []byte("both\r\n\x00raw bytes\n"), nil
	}}
}

func TestPlanUnresolvedMergePreservesCanonicalState(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			r, original, incoming := unresolvedMergeFixture(t, format)
			if format == "sha256" {
				mustRun(t, r, "", "update-index", "--split-index")
			}
			putPublicationFile(t, r.dir, "state", "later unstaged edit\r\n")
			putPublicationFile(t, r.dir, "pending", "untracked pending bytes\n")
			mustRun(t, r, "", "config", "core.fsmonitor", "sh -c 'echo ran > fsmonitor-ran; exit 1' --")
			mustRun(t, r, "", "config", "core.hooksPath", filepath.Join(r.dir, "hooks"))
			putPublicationFile(t, r.dir, "hooks/reference-transaction", "#!/bin/sh\necho ran > hook-ran\nexit 99\n")
			if err := os.Chmod(filepath.Join(r.dir, "hooks/reference-transaction"), 0755); err != nil {
				t.Fatal(err)
			}
			before := map[string][]byte{}
			for _, path := range []string{".git/HEAD", ".git/index", ".git/config", ".git/MERGE_HEAD", ".git/ORIG_HEAD", ".git/MERGE_MSG", "state", "pending"} {
				before[path] = readMergeTestFile(t, r.dir, path)
			}
			t.Setenv("GIT_DIR", t.TempDir())
			t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "wrong"))
			p := mergePlanPolicy(t)
			resolve := p.Resolve
			p.Resolve = func(ctx context.Context, path string, base, ours, theirs []byte, files RelatedFiles) ([]byte, error) {
				data, err := files.Read(ctx, OurSide, "clean")
				if err != nil || string(data) != "nonconflicting local bytes\n" {
					t.Fatalf("wrong related bytes: %q %v", data, err)
				}
				if err := files.Add(ctx, "parts/new", data); err != nil {
					return nil, err
				}
				return resolve(ctx, path, base, ours, theirs, files)
			}
			calls := 0
			p.Audit = func(context.Context, string) error { calls++; return nil }
			dest := filepath.Join(t.TempDir(), "plan")
			plan, err := PlanUnresolvedMerge(t.Context(), r.dir, dest, p)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(before[".git/index"])
			if plan.Original != original || plan.Incoming != incoming || calls != 3 || plan.IndexDigest != hex.EncodeToString(digest[:]) {
				t.Fatalf("wrong plan: %+v audits=%d", plan, calls)
			}
			for path, want := range before {
				if !bytes.Equal(readMergeTestFile(t, r.dir, path), want) {
					t.Fatalf("changed canonical %s", path)
				}
			}
			for _, path := range []string{"hook-ran", "fsmonitor-ran"} {
				if _, err := os.Stat(filepath.Join(r.dir, path)); !os.IsNotExist(err) {
					t.Fatal("ran configured program", path, err)
				}
			}
			if mustRun(t, r, "", "rev-parse", "HEAD") != original {
				t.Fatal("moved canonical HEAD")
			}
			if _, err := r.run(t.Context(), nil, "cat-file", "-e", plan.Commit); err == nil {
				t.Fatal("wrote candidate to canonical object store")
			}
			entries, err := os.ReadDir(dest)
			if err != nil || len(entries) != 1 || entries[0].Name() != "merge.bundle" {
				t.Fatalf("left private workspace: %v %v", entries, err)
			}
			// The plan remains inspectable after the source repository disappears.
			if err := os.RemoveAll(r.dir); err != nil {
				t.Fatal(err)
			}
			verify, err := initRepo(t.Context(), filepath.Join(t.TempDir(), "verify"), plan.Commit)
			if err != nil {
				t.Fatal(err)
			}
			if err := verify.importRef(t.Context(), plan.BundlePath, "refs/rig/merge-plan", "refs/rig/merge-plan", plan.Commit); err != nil {
				t.Fatal(err)
			}
			if got := mustRun(t, verify, "", "show", "-s", "--format=%T%n%P", plan.Commit); got != plan.Tree+"\n"+original+" "+incoming {
				t.Fatal("wrong candidate ancestry", got)
			}
			got, err := verify.run(t.Context(), nil, "show", plan.Commit+":state")
			if err != nil || got != "both\r\n\x00raw bytes\n" {
				t.Fatalf("changed planned bytes: %q %v", got, err)
			}
			for _, path := range []string{"clean", "parts/new"} {
				if got := mustRun(t, verify, "", "show", plan.Commit+":"+path); got != "nonconflicting local bytes" {
					t.Fatal("lost nonconflicting or related file", path, got)
				}
			}
		})
	}
}

func TestPlanUnresolvedMergeRefusesUnsafeOrChangedInputs(t *testing.T) {
	for _, kind := range []string{"partial-resolution", "fully-resolved", "extra-staged", "bisect", "autostash", "wrong-original", "missing-marker", "declined", "audit-parent", "audit-result", "audit-mutation", "tree-limit", "bundle-limit", "changed-head", "changed-index", "new-operation", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			r, original, incoming := unresolvedMergeFixture(t, "sha1")
			p := mergePlanPolicy(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch kind {
			case "partial-resolution":
				mustRun(t, r, "", "merge", "--abort")
				mustRun(t, r, "", "checkout", "incoming")
				putPublicationFile(t, r.dir, "second", "incoming conflict")
				mustRun(t, r, "", "add", "second")
				mustRun(t, r, "", "commit", "-m", "second incoming conflict")
				incoming = mustRun(t, r, "", "rev-parse", "HEAD")
				mustRun(t, r, "", "checkout", "main")
				putPublicationFile(t, r.dir, "second", "local conflict")
				mustRun(t, r, "", "add", "second")
				mustRun(t, r, "", "commit", "-m", "second local conflict")
				original = mustRun(t, r, "", "rev-parse", "HEAD")
				if _, err := r.run(t.Context(), nil, "merge", "--no-commit", "incoming"); !gitExited(err, 1) {
					t.Fatal(err)
				}
				fallthrough
			case "fully-resolved":
				putPublicationFile(t, r.dir, "state", "manual resolution")
				mustRun(t, r, "", "add", "state")
			case "extra-staged":
				putPublicationFile(t, r.dir, "extra", "later staged bytes")
				mustRun(t, r, "", "add", "extra")
			case "bisect":
				putPublicationFile(t, r.dir, ".git/BISECT_START", original+"\n")
			case "autostash":
				putPublicationFile(t, r.dir, ".git/MERGE_AUTOSTASH", original+"\n")
			case "wrong-original":
				putPublicationFile(t, r.dir, ".git/ORIG_HEAD", incoming+"\n")
			case "missing-marker":
				if err := os.Remove(filepath.Join(r.dir, ".git/MERGE_HEAD")); err != nil {
					t.Fatal(err)
				}
			case "declined":
				p.Resolve = func(context.Context, string, []byte, []byte, []byte, RelatedFiles) ([]byte, error) {
					return nil, ErrConflict
				}
			case "tree-limit":
				p.MaxTreeBytes = 1
			case "bundle-limit":
				p.MaxBundleBytes = 1
			case "cancel":
				resolve := p.Resolve
				p.Resolve = func(ctx context.Context, path string, base, ours, theirs []byte, files RelatedFiles) ([]byte, error) {
					cancel()
					return resolve(ctx, path, base, ours, theirs, files)
				}
			}
			before := readMergeTestFile(t, r.dir, ".git/index")
			calls := 0
			p.Audit = func(_ context.Context, tree string) error {
				calls++
				if kind == "audit-parent" || (kind == "audit-result" && calls == 3) {
					return errors.New("synthetic audit rejection")
				}
				if kind == "audit-mutation" {
					return os.WriteFile(filepath.Join(tree, "state"), []byte("policy mutation"), 0600)
				}
				if calls == 3 {
					switch kind {
					case "changed-head":
						mustRun(t, r, "", "update-ref", "HEAD", incoming, original)
						original = incoming
					case "changed-index":
						putPublicationFile(t, r.dir, "extra", "concurrent staged edit")
						mustRun(t, r, "", "add", "extra")
						before = readMergeTestFile(t, r.dir, ".git/index")
					case "new-operation":
						putPublicationFile(t, r.dir, ".git/BISECT_START", original+"\n")
					}
				}
				return nil
			}
			dest := filepath.Join(t.TempDir(), "plan")
			plan, err := PlanUnresolvedMerge(ctx, r.dir, dest, p)
			if err == nil || plan != (MergePlan{}) {
				t.Fatalf("accepted %s: %+v %v", kind, plan, err)
			}
			if kind == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if strings.HasSuffix(kind, "limit") && !errors.Is(err, artifact.ErrTooLarge) {
				t.Fatal(err)
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatal("retained failed plan", err)
			}
			if !bytes.Equal(before, readMergeTestFile(t, r.dir, ".git/index")) || mustRun(t, r, "", "rev-parse", "HEAD") != original {
				t.Fatal("failed plan modified canonical state")
			}
		})
	}
}

func TestPlanUnresolvedMergeRefusesUnsafeDestinations(t *testing.T) {
	r, _, _ := unresolvedMergeFixture(t, "sha1")
	existing := t.TempDir()
	putPublicationFile(t, existing, "keep", "caller bytes")
	for _, dest := range []string{existing, filepath.Join(r.dir, "output"), filepath.Join(r.dir, ".git", "output")} {
		if plan, err := PlanUnresolvedMerge(t.Context(), r.dir, dest, mergePlanPolicy(t)); err == nil || plan != (MergePlan{}) {
			t.Fatalf("accepted destination %s: %+v %v", dest, plan, err)
		}
	}
	if got := string(readMergeTestFile(t, existing, "keep")); got != "caller bytes" {
		t.Fatal("changed caller destination")
	}
	for _, path := range []string{"output", ".git/output"} {
		if _, err := os.Stat(filepath.Join(r.dir, path)); !os.IsNotExist(err) {
			t.Fatal("wrote into canonical state", err)
		}
	}
}

func TestPlanUnresolvedMergeLinkedWorktree(t *testing.T) {
	r, original, _ := unresolvedMergeFixture(t, "sha1")
	linked := filepath.Join(t.TempDir(), "linked")
	sibling := filepath.Join(t.TempDir(), "sibling checkout")
	mustRun(t, r, "", "worktree", "add", "--detach", sibling, original)
	mustRun(t, r, "", "worktree", "add", "--detach", linked, original)
	worktree := r
	worktree.dir = linked
	if _, err := worktree.run(t.Context(), nil, "merge", "--no-commit", "incoming"); !gitExited(err, 1) {
		t.Fatal("expected linked conflict", err)
	}
	for _, dest := range []string{filepath.Join(r.dir, ".git", "plan"), filepath.Join(r.dir, "plan"), filepath.Join(linked, "plan"), filepath.Join(sibling, "plan")} {
		if _, err := PlanUnresolvedMerge(t.Context(), linked, dest, mergePlanPolicy(t)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted canonical destination %s: %v", dest, err)
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatal("wrote output into a registered worktree", dest, err)
		}
	}
	if _, err := PlanUnresolvedMerge(t.Context(), linked, filepath.Join(t.TempDir(), "plan"), mergePlanPolicy(t)); err != nil {
		t.Fatal(err)
	}
	if mustRun(t, worktree, "", "rev-parse", "HEAD") != original {
		t.Fatal("moved linked HEAD")
	}
	t.Run("destination-alias", func(t *testing.T) {
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(linked, alias); err != nil {
			t.Skip("symlink unavailable", err)
		}
		if _, err := PlanUnresolvedMerge(t.Context(), linked, filepath.Join(alias, "plan"), mergePlanPolicy(t)); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted alias into canonical tree", err)
		}
	})
}

func TestMergePlanPathsOverlap(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	for _, tc := range []struct {
		name, a, b string
		want       bool
	}{
		{"volume root", root, filepath.Join(root, "checkout", "plan"), true},
		{"same path", filepath.Join(root, "checkout"), filepath.Join(root, "checkout"), true},
		{"descendant", filepath.Join(root, "checkout"), filepath.Join(root, "checkout", "plan"), true},
		{"sibling prefix", filepath.Join(root, "checkout"), filepath.Join(root, "checkout-plan"), false},
		{"case alias", filepath.Join(root, "Checkout"), filepath.Join(root, "checkout", "plan"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if mergePlanPathsOverlap(tc.a, tc.b) != tc.want || mergePlanPathsOverlap(tc.b, tc.a) != tc.want {
				t.Fatalf("incorrect overlap for %q and %q", tc.a, tc.b)
			}
		})
	}
}
