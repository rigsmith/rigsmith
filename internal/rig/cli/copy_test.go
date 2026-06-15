package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureWritableDest(t *testing.T) {
	base := t.TempDir()

	// Absent path: ok.
	if err := ensureWritableDest(filepath.Join(base, "new")); err != nil {
		t.Errorf("absent path should be allowed: %v", err)
	}

	// Empty directory: ok.
	empty := filepath.Join(base, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureWritableDest(empty); err != nil {
		t.Errorf("empty dir should be allowed: %v", err)
	}

	// Populated directory: refused.
	full := filepath.Join(base, "full")
	if err := os.MkdirAll(filepath.Join(full, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureWritableDest(full); err == nil {
		t.Error("populated dir should be refused")
	}

	// Existing file: refused.
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureWritableDest(file); err == nil {
		t.Error("existing file should be refused")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:           "0 B",
		512:         "512 B",
		1024:        "1.0 KB",
		1536:        "1.5 KB",
		1024 * 1024: "1.0 MB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
