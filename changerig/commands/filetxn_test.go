package commands

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFileTxnRollbackRestoresExisting verifies guard()+rollback() restores a
// pre-existing file's original bytes after it was mutated.
func TestFileTxnRollbackRestoresExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}

	txn := newFileTxn()
	if err := txn.guard(path); err != nil {
		t.Fatalf("guard: %v", err)
	}
	if err := os.WriteFile(path, []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}
	txn.rollback()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after rollback: %v", err)
	}
	if string(got) != "A" {
		t.Fatalf("content = %q, want %q", got, "A")
	}
}

// TestFileTxnRollbackRemovesCreated verifies rollback() removes a file that did
// not exist when it was guarded.
func TestFileTxnRollbackRemovesCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")

	txn := newFileTxn()
	if err := txn.guard(path); err != nil {
		t.Fatalf("guard: %v", err)
	}
	if err := os.WriteFile(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	txn.rollback()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stat after rollback: err = %v, want IsNotExist", err)
	}
}

// TestFileTxnGuardFirstSnapshotWins verifies guard() is idempotent: re-guarding
// a path keeps the first snapshot, so rollback restores the earliest state.
func TestFileTxnGuardFirstSnapshotWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}

	txn := newFileTxn()
	if err := txn.guard(path); err != nil {
		t.Fatalf("guard: %v", err)
	}
	if err := os.WriteFile(path, []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := txn.guard(path); err != nil {
		t.Fatalf("guard (second): %v", err)
	}
	if err := os.WriteFile(path, []byte("C"), 0o644); err != nil {
		t.Fatal(err)
	}
	txn.rollback()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after rollback: %v", err)
	}
	if string(got) != "A" {
		t.Fatalf("content = %q, want %q (first snapshot)", got, "A")
	}
}

// TestFileTxnRollbackMultipleFiles verifies the all-or-nothing intent: guarding
// several files and rolling back restores every one of them.
func TestFileTxnRollbackMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "one.txt")
	p2 := filepath.Join(dir, "two.txt")
	if err := os.WriteFile(p1, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p2, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}

	txn := newFileTxn()
	for _, p := range []string{p1, p2} {
		if err := txn.guard(p); err != nil {
			t.Fatalf("guard %s: %v", p, err)
		}
	}
	if err := os.WriteFile(p1, []byte("ONE-MUT"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p2, []byte("TWO-MUT"), 0o644); err != nil {
		t.Fatal(err)
	}
	txn.rollback()

	for _, tc := range []struct{ path, want string }{{p1, "one"}, {p2, "two"}} {
		got, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		if string(got) != tc.want {
			t.Fatalf("%s = %q, want %q", tc.path, got, tc.want)
		}
	}
}
