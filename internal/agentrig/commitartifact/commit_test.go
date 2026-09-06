package commitartifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

func captureRequest(t *testing.T) Request {
	t.Helper()
	root := t.TempDir()
	captures := artifact.Store{Dir: filepath.Join(root, "captures")}
	ref, err := captures.Build(t.Context(), artifact.Key([]byte("fixture")), func(_ context.Context, tree string) error {
		for path, data := range map[string]string{
			"payload": "original\r\n\x00bytes\r\n", ".gitattributes": "* text filter=hostile\n",
			".gitignore": "ignored\n", "ignored": "keep ignored bytes", "executable": "#!/bin/sh\n",
		} {
			mode := os.FileMode(0600)
			if path == "executable" {
				mode = 0700
			}
			if err := os.WriteFile(filepath.Join(tree, path), []byte(data), mode); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return Request{Captures: captures, Commits: artifact.Store{Dir: filepath.Join(root, "commits")}, CaptureRef: ref,
		PolicyID: "fixture-v1", Message: "fixture capture", AuthorName: "fixture", AuthorEmail: "fixture@example.com",
		Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Prepare: func(string) error { return nil }, Audit: func(string) error { return nil }}
}

func TestRetainedCommitPreservesRawBytesAndReplaysWithoutInputs(t *testing.T) {
	r := captureRequest(t)
	// Inherited Git redirection/config must not affect the private writer.
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "wrong"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.autocrlf")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	t.Setenv("GIT_AUTHOR_NAME", "wrong identity")
	ref, err := Build(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Open(t.Context(), r.Commits, ref, filepath.Join(t.TempDir(), "opened"))
	if err != nil {
		t.Fatal(err)
	}
	if info.CaptureRef != r.CaptureRef || info.Parent != "" {
		t.Fatalf("wrong commit: %+v", info)
	}
	repo, err := initRepo(t.Context(), filepath.Join(t.TempDir(), "git"), info.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.run(t.Context(), nil, "fetch", info.BundlePath, RefName+":"+RefName); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"payload": "original\r\n\x00bytes\r\n", "ignored": "keep ignored bytes"} {
		got, err := repo.run(t.Context(), nil, "show", info.Commit+":"+path)
		if err != nil || got != want {
			t.Fatalf("%s: %q, %v", path, got, err)
		}
	}
	if got, err := repo.run(t.Context(), nil, "show", "-s", "--format=%an <%ae>", info.Commit); err != nil || strings.TrimSpace(got) != "fixture <fixture@example.com>" {
		t.Fatalf("wrong identity: %q %v", got, err)
	}
	if err := os.RemoveAll(r.Captures.Dir); err != nil {
		t.Fatal(err)
	}
	r.Prepare = func(string) error { t.Fatal("retry rebuilt commit"); return nil }
	again, err := Build(t.Context(), r)
	if err != nil || again != ref {
		t.Fatalf("retry: %q %v", again, err)
	}
	if _, err := Open(t.Context(), r.Commits, ref, filepath.Join(t.TempDir(), "reopened")); err != nil {
		t.Fatal(err)
	}
}

func TestRetainedCommitDeterministicBeforeMarker(t *testing.T) {
	r := captureRequest(t)
	ref, err := Build(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Open(t.Context(), r.Commits, ref, filepath.Join(t.TempDir(), "first"))
	if err != nil {
		t.Fatal(err)
	}
	// Rebuilding prior to any durable commit marker gives the same Git identity.
	r.Commits.Dir = filepath.Join(t.TempDir(), "other-store")
	other, err := Build(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(t.Context(), r.Commits, other, filepath.Join(t.TempDir(), "second"))
	if err != nil || first.Commit != second.Commit {
		t.Fatalf("commit changed: %+v %+v %v", first, second, err)
	}
}

func TestRetainedCommitFailuresDoNotSeal(t *testing.T) {
	for _, mode := range []string{"audit", "prepare", "link", "capacity", "cancel", "missing-capture", "missing-policy"} {
		t.Run(mode, func(t *testing.T) {
			r := captureRequest(t)
			ctx := t.Context()
			switch mode {
			case "audit":
				r.Audit = func(string) error { return errors.New("audit denied") }
			case "prepare":
				r.Prepare = func(string) error { return errors.New("prepare denied") }
			case "link":
				r.Prepare = func(tree string) error {
					if err := os.Symlink("payload", filepath.Join(tree, "link")); err != nil {
						t.Skipf("symlinks unavailable: %v", err)
					}
					return nil
				}
			case "capacity":
				r.Commits.MaxBytes = 512
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "missing-capture":
				if err := os.RemoveAll(r.Captures.Dir); err != nil {
					t.Fatal(err)
				}
			case "missing-policy":
				r.Audit = nil
			}
			if ref, err := Build(ctx, r); err == nil || ref != "" {
				t.Fatalf("invalid build succeeded: %s %v", ref, err)
			}
			if paths, _ := filepath.Glob(filepath.Join(r.Commits.Dir, "*.capture")); len(paths) != 0 {
				t.Fatal("failed commit sealed", paths)
			}
			if paths, _ := filepath.Glob(filepath.Join(r.Commits.Dir, ".capture-work-*")); len(paths) != 0 {
				t.Fatal("workspace leaked", paths)
			}
		})
	}
}

func TestCorruptRetainedCommitNeverRebuilds(t *testing.T) {
	r := captureRequest(t)
	ref, err := Build(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	key, _, _ := strings.Cut(ref, ":")
	if err := os.WriteFile(filepath.Join(r.Commits.Dir, key+".capture"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	r.Prepare = func(string) error { t.Fatal("corrupt output rebuilt"); return nil }
	if _, err := Build(t.Context(), r); err == nil {
		t.Fatal("corrupt retry succeeded")
	}
	dest := filepath.Join(t.TempDir(), "bad")
	if _, err := Open(t.Context(), r.Commits, ref, dest); err == nil {
		t.Fatal("corrupt output opened")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("failed open left destination", err)
	}
}

func TestOpenRejectsForgedBundleMetadata(t *testing.T) {
	r := captureRequest(t)
	ref, err := r.Commits.BuildWithMetadata(t.Context(), artifact.Key([]byte("forged")), func(_ context.Context, tree string, meta *artifact.Metadata) error {
		meta.BaseReference = strings.Repeat("a", 40)
		if err := os.WriteFile(filepath.Join(tree, "commit.bundle"), []byte("not a bundle"), 0600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(tree, "commit.json"), []byte(`{"Commit":"`+meta.BaseReference+`","Tree":"`+meta.BaseReference+`"}`), 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "bad")
	if _, err := Open(t.Context(), r.Commits, ref, dest); err == nil {
		t.Fatal("forged bundle opened")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("failed open left destination", err)
	}
}
