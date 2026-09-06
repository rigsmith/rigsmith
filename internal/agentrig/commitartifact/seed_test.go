package commitartifact

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

func seedRepository(t *testing.T, format string) (gitRepo, string) {
	t.Helper()
	hint := ""
	if format == "sha256" {
		hint = strings.Repeat("0", 64)
	}
	repo, err := initRepo(t.Context(), filepath.Join(t.TempDir(), "git"), hint)
	if err != nil {
		t.Fatal(err)
	}
	repo.identity = []string{"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.com", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.com", "GIT_AUTHOR_DATE=@1700000000 +0000", "GIT_COMMITTER_DATE=@1700000000 +0000"}
	tree := t.TempDir()
	var parent string
	for _, body := range []string{"first history entry", "second history entry"} {
		if err := os.WriteFile(filepath.Join(tree, "history.txt"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		treeID, err := repo.writeTree(t.Context(), tree, tree, nil)
		if err != nil {
			t.Fatal(err)
		}
		args := []string{"commit-tree", treeID}
		if parent != "" {
			args = append(args, "-p", parent)
		}
		sha, err := repo.run(t.Context(), strings.NewReader(body+"\n"), args...)
		if err != nil {
			t.Fatal(err)
		}
		parent = strings.TrimSpace(sha)
	}
	if _, err := repo.run(t.Context(), nil, "update-ref", "refs/heads/main", parent); err != nil {
		t.Fatal(err)
	}
	return repo, parent
}

func TestRetainedSeedSurvivesSourceRemovalAndSharesAncestry(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			repo, sha := seedRepository(t, format)
			captures := artifact.Store{Dir: filepath.Join(t.TempDir(), "captures")}
			ref, err := RetainSeed(t.Context(), captures, repo.dir, sha)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(repo.dir); err != nil {
				t.Fatal(err)
			}
			again, err := RetainSeed(t.Context(), captures, repo.dir, sha)
			if err != nil || again != ref {
				t.Fatalf("seed retry needed source: %s %v", again, err)
			}
			if paths, _ := filepath.Glob(filepath.Join(SeedStore(captures).Dir, "*.capture")); len(paths) != 1 {
				t.Fatalf("seed was not deduplicated: %v", paths)
			}
			copy, err := initRepo(t.Context(), filepath.Join(t.TempDir(), "git"), sha)
			if err != nil {
				t.Fatal(err)
			}
			if err := copy.loadSeed(t.Context(), captures, ref, sha, filepath.Join(t.TempDir(), "seed")); err != nil {
				t.Fatal(err)
			}
			count, err := copy.run(t.Context(), nil, "rev-list", "--count", seedRefName)
			if err != nil || strings.TrimSpace(count) != "2" {
				t.Fatalf("ancestry missing: %s %v", count, err)
			}
		})
	}
}

func TestSeedFailureCannotProduceCaptureReference(t *testing.T) {
	for _, mode := range []string{"missing-object", "capacity", "canceled", "corrupt", "mismatched", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			repo, sha := seedRepository(t, "sha1")
			r := captureRequest(t)
			switch mode {
			case "missing-object":
				if ref, err := RetainSeed(t.Context(), r.Captures, repo.dir, strings.Repeat("a", 40)); err == nil || ref != "" {
					t.Fatalf("missing object retained: %s %v", ref, err)
				}
				return
			case "capacity":
				captures := r.Captures
				captures.MaxBytes = 512
				if ref, err := RetainSeed(t.Context(), captures, repo.dir, sha); !errors.Is(err, artifact.ErrTooLarge) || ref != "" {
					t.Fatalf("oversized seed retained: %s %v", ref, err)
				}
				return
			case "canceled":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if ref, err := RetainSeed(ctx, r.Captures, repo.dir, sha); !errors.Is(err, context.Canceled) || ref != "" {
					t.Fatalf("canceled seed retained: %s %v", ref, err)
				}
				return
			}
			seed, err := RetainSeed(t.Context(), r.Captures, repo.dir, sha)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "corrupt" {
				key, _, _ := strings.Cut(seed, ":")
				if err := os.WriteFile(filepath.Join(SeedStore(r.Captures).Dir, key+".capture"), []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := RetainSeed(t.Context(), r.Captures, repo.dir, sha); err == nil {
					t.Fatal("corrupt seed rebuilt from live source")
				}
			}
			if mode == "mismatched" {
				sha = strings.Repeat("b", 40)
			}
			if mode == "legacy" {
				seed = ""
			}
			r.CaptureRef, err = r.Captures.BuildWithMetadata(t.Context(), artifact.Key([]byte(mode)), func(_ context.Context, tree string, meta *artifact.Metadata) error {
				meta.BaseReference, meta.SeedReference = sha, seed
				return os.WriteFile(filepath.Join(tree, "payload"), []byte("captured"), 0600)
			})
			if err != nil {
				t.Fatal(err)
			}
			if ref, err := Build(t.Context(), r); err == nil || ref != "" {
				t.Fatalf("invalid seed committed: %s %v", ref, err)
			}
			if paths, _ := filepath.Glob(filepath.Join(r.Commits.Dir, "*.capture")); len(paths) != 0 {
				t.Fatal("failed commit sealed", paths)
			}
		})
	}
}

func TestRetainSeedRejectsIncompleteShallowHistory(t *testing.T) {
	repo, sha := seedRepository(t, "sha1")
	shallow := filepath.Join(t.TempDir(), "shallow.git")
	sourcePath := filepath.ToSlash(repo.dir)
	if !strings.HasPrefix(sourcePath, "/") {
		sourcePath = "/" + sourcePath
	}
	url := (&url.URL{Scheme: "file", Path: sourcePath}).String()
	if _, err := repo.run(t.Context(), nil, "clone", "--bare", "--depth=1", url, shallow); err != nil {
		t.Fatal(err)
	}
	captures := artifact.Store{Dir: filepath.Join(t.TempDir(), "captures")}
	if ref, err := RetainSeed(t.Context(), captures, shallow, sha); err == nil || ref != "" {
		t.Fatalf("incomplete seed history accepted: %s %v", ref, err)
	}
	if paths, _ := filepath.Glob(filepath.Join(SeedStore(captures).Dir, "*.capture")); len(paths) != 0 {
		t.Fatal("incomplete seed sealed", paths)
	}
}
