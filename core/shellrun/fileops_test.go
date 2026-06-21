package shellrun

import (
	"os"
	"path/filepath"
	"testing"
)

// These exercise the Go-backed cp/mv/rm/mkdir through the portable shell against
// a temp dir, so the same behaviour holds on Windows (pure os calls, no coreutils).

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func TestFileOpMkdirP(t *testing.T) {
	dir := t.TempDir()
	if _, code, _ := runPortable(t, nil, "mkdir -p a/b/c", dir); code != 0 {
		t.Fatalf("mkdir -p code=%d", code)
	}
	if !isDir(filepath.Join(dir, "a/b/c")) {
		t.Error("mkdir -p did not create the nested path")
	}
	// Without -p, a missing parent is an error.
	if _, code, _ := runPortable(t, nil, "mkdir x/y", dir); code == 0 {
		t.Error("mkdir without -p should fail on a missing parent")
	}
}

func TestFileOpCpFileAndIntoDir(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "src.txt"), "hello")
	mustWrite(t, filepath.Join(dir, "into", "keep"), "")

	if _, code, _ := runPortable(t, nil, "cp src.txt dst.txt", dir); code != 0 {
		t.Fatalf("cp file code=%d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "dst.txt")); string(b) != "hello" {
		t.Errorf("dst.txt = %q, want hello", b)
	}

	// cp into an existing directory keeps the basename.
	if _, code, _ := runPortable(t, nil, "cp src.txt into", dir); code != 0 {
		t.Fatalf("cp into dir code=%d", code)
	}
	if !exists(filepath.Join(dir, "into", "src.txt")) {
		t.Error("cp into dir did not place src.txt inside it")
	}
}

func TestFileOpCpRecursive(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "tree", "a.txt"), "a")
	mustWrite(t, filepath.Join(dir, "tree", "sub", "b.txt"), "b")

	// Without -r, copying a directory is an error.
	if out, code, _ := runPortable(t, nil, "cp tree copy", dir); code == 0 {
		t.Errorf("cp dir without -r should fail; out=%v", out)
	}

	if _, code, _ := runPortable(t, nil, "cp -r tree copy", dir); code != 0 {
		t.Fatalf("cp -r code=%d", code)
	}
	if !exists(filepath.Join(dir, "copy", "a.txt")) || !exists(filepath.Join(dir, "copy", "sub", "b.txt")) {
		t.Error("cp -r did not copy the whole tree")
	}
}

func TestFileOpCpRequiresExistingDestParent(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "src.txt"), "x")

	// Like coreutils, copying to a path whose parent doesn't exist is an error,
	// not a silent mkdir — so a typo'd path fails loudly.
	if _, code, _ := runPortable(t, nil, "cp src.txt missing/dst.txt", dir); code == 0 {
		t.Error("cp to a missing parent dir should fail")
	}
	if exists(filepath.Join(dir, "missing")) {
		t.Error("cp must not create the destination's parent directory")
	}
}

func TestFileOpMv(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "old.txt"), "x")

	if _, code, _ := runPortable(t, nil, "mv old.txt new.txt", dir); code != 0 {
		t.Fatalf("mv code=%d", code)
	}
	if exists(filepath.Join(dir, "old.txt")) || !exists(filepath.Join(dir, "new.txt")) {
		t.Error("mv did not move the file")
	}
}

func TestFileOpRm(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "f.txt"), "x")
	mustWrite(t, filepath.Join(dir, "d", "nested.txt"), "y")

	if _, code, _ := runPortable(t, nil, "rm f.txt", dir); code != 0 || exists(filepath.Join(dir, "f.txt")) {
		t.Errorf("rm file: code=%d still-exists=%v", code, exists(filepath.Join(dir, "f.txt")))
	}
	// rm a directory needs -r.
	if _, code, _ := runPortable(t, nil, "rm d", dir); code == 0 {
		t.Error("rm on a directory without -r should fail")
	}
	if _, code, _ := runPortable(t, nil, "rm -rf d", dir); code != 0 || exists(filepath.Join(dir, "d")) {
		t.Errorf("rm -rf dir: code=%d still-exists=%v", code, exists(filepath.Join(dir, "d")))
	}
	// rm -f on a missing path succeeds.
	if _, code, _ := runPortable(t, nil, "rm -f does-not-exist", dir); code != 0 {
		t.Errorf("rm -f missing should succeed, code=%d", code)
	}
}

func TestFileOpRmRecursiveMissingFailsWithoutForce(t *testing.T) {
	dir := t.TempDir()
	// `rm -r missing` must fail (typo protection); `rm -rf missing` succeeds.
	if _, code, _ := runPortable(t, nil, "rm -r nope", dir); code == 0 {
		t.Error("rm -r on a missing path should fail without -f")
	}
	if _, code, _ := runPortable(t, nil, "rm -rf nope", dir); code != 0 {
		t.Error("rm -rf on a missing path should succeed")
	}
}

func TestFileOpCpRecursiveRequiresExistingDestParent(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "src", "a.txt"), "a")

	if _, code, _ := runPortable(t, nil, "cp -r src missing/dst", dir); code == 0 {
		t.Error("cp -r into a missing dest parent should fail")
	}
	if exists(filepath.Join(dir, "missing")) {
		t.Error("cp -r must not create the missing dest parent directory")
	}
}

func TestFileOpsComposeInOneScript(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "build", "app"), "binary")

	// A realistic one-liner a custom step might run — all portable.
	script := "mkdir -p dist && cp -r build/* dist/ && mv dist/app dist/app-v1 && rm -rf build"
	if out, code, _ := runPortable(t, nil, script, dir); code != 0 {
		t.Fatalf("compose script code=%d out=%v", code, out)
	}
	if !exists(filepath.Join(dir, "dist", "app-v1")) {
		t.Error("expected dist/app-v1 after the pipeline")
	}
	if exists(filepath.Join(dir, "build")) {
		t.Error("expected build/ removed")
	}
}

func TestFileOpsFallThroughToExec(t *testing.T) {
	// A non-file-op command still reaches the default exec handler.
	if out, code, _ := runPortable(t, nil, "echo hello", t.TempDir()); code != 0 || len(out) == 0 || out[0] != "hello" {
		t.Errorf("echo should still work: out=%v code=%d", out, code)
	}
}
