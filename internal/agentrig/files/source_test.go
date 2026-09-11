package files

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func sourceFixture(t *testing.T) (string, *Source) {
	t.Helper()
	root := t.TempDir()
	s, err := OpenSource(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return root, s
}

func TestSourceReadAndNames(t *testing.T) {
	root, s := sourceFixture(t)
	for name, body := range map[string]string{"z": "last", "a": "first"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	names, err := s.Names(t.Context(), 2)
	if err != nil || !reflect.DeepEqual(names, []string{"a", "z"}) {
		t.Fatalf("names=%v err=%v", names, err)
	}
	data, err := s.Read(t.Context(), "a", 5)
	if err != nil || string(data) != "first" {
		t.Fatalf("read=%q err=%v", data, err)
	}
	if data, err := s.Read(t.Context(), "a", 4); !errors.Is(err, ErrSourceLimit) || data != nil {
		t.Fatalf("size limit: %v", err)
	}
	if names, err := s.Names(t.Context(), 1); !errors.Is(err, ErrSourceLimit) || names != nil {
		t.Fatalf("name limit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "empty"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := s.Read(t.Context(), "empty", 1); err != nil || len(data) != 0 {
		t.Fatalf("empty: %v", err)
	}
}

func TestSourceRejectsPathsLinksAndSpecialShapes(t *testing.T) {
	root, s := sourceFixture(t)
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".", "..", "../outside", "nested/file", `nested\file`, "file:stream", "directory", "/absolute"} {
		if data, err := s.Read(t.Context(), name, 100); !errors.Is(err, ErrSource) || data != nil {
			t.Fatalf("accepted %q: %v", name, err)
		}
	}
	if data, err := s.Read(t.Context(), "missing", 100); !errors.Is(err, os.ErrNotExist) || data != nil {
		t.Fatalf("missing: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if data, err := s.Read(t.Context(), "link", 100); !errors.Is(err, ErrSource) || data != nil {
		t.Fatalf("followed link: %v", err)
	}
	if _, err := OpenSource(t.Context(), filepath.Join(root, "link")); !errors.Is(err, ErrSource) {
		t.Fatalf("opened file link: %v", err)
	}
	dirLink := filepath.Join(t.TempDir(), "dir-link")
	if err := os.Symlink(root, dirLink); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSource(t.Context(), dirLink); !errors.Is(err, ErrSource) {
		t.Fatalf("opened directory link: %v", err)
	}
}

func TestSourceCancellationAndRootReplacement(t *testing.T) {
	root, s := sourceFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := OpenSource(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.Names(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.Read(ctx, "a", 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := OpenSource(t.Context(), "relative"); !errors.Is(err, ErrSource) {
		t.Fatal(err)
	}
	moved := root + "-moved"
	if err := os.Rename(root, moved); err != nil {
		t.Skipf("platform does not permit moving open directory: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(moved) })
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Check(t.Context()); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("root replacement not detected: %v", err)
	}
	if _, err := s.Read(t.Context(), "a", 1); !errors.Is(err, ErrSourceChanged) {
		t.Fatal(err)
	}
}

// Run a deterministic concurrent writer/cancellation at a read checkpoint.
// This avoids racing the scheduler or requiring enormous source fixtures.
type checkpointContext struct {
	context.Context
	calls  int
	at     int
	action func()
}

func (c *checkpointContext) Err() error {
	c.calls++
	if c.calls == c.at {
		c.action()
	}
	return c.Context.Err()
}

func TestSourceRejectsChangeBetweenReadBlocks(t *testing.T) {
	root, s := sourceFixture(t)
	path := filepath.Join(root, "config")
	original := bytes.Repeat([]byte("a"), 96<<10)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := &checkpointContext{Context: t.Context(), at: 3, action: func() {
		if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
	}}
	if data, err := s.Read(ctx, "config", int64(len(original))); !errors.Is(err, ErrSourceChanged) || data != nil {
		t.Fatalf("partial read accepted: %v", err)
	}
}

func TestSourceCancellationBetweenReadBlocks(t *testing.T) {
	root, s := sourceFixture(t)
	if err := os.WriteFile(filepath.Join(root, "config"), bytes.Repeat([]byte("a"), 96<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &checkpointContext{Context: base, at: 3, action: cancel}
	if data, err := s.Read(ctx, "config", 1<<20); !errors.Is(err, context.Canceled) || data != nil {
		t.Fatalf("partial read returned: %v", err)
	}
}
