package durable

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFailedReplacementKeepsOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("serialization failed")
	if err := Write(t.Context(), path, func(f *os.File) error { f.WriteString("partial"); return cause }); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	if err := Write(ctx, path, func(f *os.File) error { f.WriteString("cancelled"); cancel(); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "old" {
		t.Fatal("old state changed", err)
	}
	if err = Rewrite(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path)
	if err != nil || string(b) != "old" {
		t.Fatal("reflush changed bytes", err)
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(files) != 1 {
		t.Fatal("temporary files leaked", err)
	}
}
