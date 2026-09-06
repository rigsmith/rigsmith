package files_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/files"
)

var private = files.Permissions{Dir: 0o700, File: 0o600}

func put(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := files.Write(p, []byte(body), private); err != nil {
		t.Fatal(err)
	}
	return p
}
func body(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func link(t *testing.T, dst, target string) {
	t.Helper()
	if err := os.Symlink(target, dst); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
}
func copier(src, dst, _, _ string) error { return files.Copy(src, dst, private) }

func TestSnapshotBytesMtimeAndFailure(t *testing.T) {
	src, target := t.TempDir(), t.TempDir()
	payload := "\x00native\r\nbytes\xff"
	source := put(t, src, "input", payload)
	dst := filepath.Join(target, "snapshot")
	stamp := time.Unix(1700000000, 0)
	if err := files.CopySnapshot(source, dst, stamp); err != nil {
		t.Fatal(err)
	}
	if got := body(t, target, "snapshot"); got != payload {
		t.Fatalf("bytes = %q", got)
	}
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(stamp) {
		t.Fatalf("mtime = %v", st.ModTime())
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", st.Mode())
	}
	// A directory can be opened but cannot be copied as file bytes. The failed
	// stream must not destroy the previous snapshot or leave a temporary file.
	if err := files.CopySnapshot(src, dst, stamp); err == nil {
		t.Fatal("expected read error")
	}
	if got := body(t, target, "snapshot"); got != payload {
		t.Fatalf("old snapshot lost: %q", got)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary files left: %v", entries)
	}
	if err := files.WriteMtime(dst, []byte("scanned"), stamp); err != nil {
		t.Fatal(err)
	}
	st, err = os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(stamp) || body(t, target, "snapshot") != "scanned" {
		t.Fatal("direct staged write differs")
	}
}

func TestReconcileOnlyRemovesRejectedPaths(t *testing.T) {
	root := t.TempDir()
	put(t, root, "retained/other-machine", "")
	put(t, root, "retained/data", "bytes")
	put(t, root, "retired/nested/key", "old")
	count, err := files.Reconcile(root, func(rel string) bool { return strings.HasPrefix(rel, "retained/") })
	if err != nil || count != 1 {
		t.Fatalf("reconcile = %d, %v", count, err)
	}
	if body(t, root, "retained/other-machine") != "" {
		t.Fatal("empty file changed")
	}
	if _, err := os.Stat(filepath.Join(root, "retired")); !os.IsNotExist(err) {
		t.Fatalf("empty tree remains: %v", err)
	}
	if !files.DirExists(root) {
		t.Fatal("root removed")
	}
	count, err = files.Reconcile(filepath.Join(root, "missing"), func(string) bool { t.Fatal("called for missing tree"); return false })
	if count != 0 || err != nil {
		t.Fatalf("missing tree = %d, %v", count, err)
	}
}

func TestRestorePolicyAndPrune(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	put(t, source, "cache/part", "storage only")
	put(t, source, "old/live", "stale")
	put(t, source, "old/ready", "encoded")
	put(t, source, "config/collision", "file")
	put(t, source, "unmanaged/parent/child", "file")
	put(t, target, "sessions/live", "newer")
	put(t, target, "config/collision/local", "keep directory contents")
	put(t, target, "unmanaged/parent", "local parent file")
	put(t, target, "config/obsolete", "delete")
	put(t, target, "unmanaged/local", "keep")
	var calls []string
	result, err := files.Restore(files.RestoreOptions{
		SourceDir: source, TargetDir: target,
		Plan: func(rel string) (string, bool) {
			return strings.Replace(rel, "old/", "sessions/", 1), strings.HasPrefix(rel, "cache/")
		},
		Live: map[string]bool{"sessions/live": true},
		Write: func(src, dst, rel, targetRel string) error {
			calls = append(calls, rel+"->"+targetRel)
			b, err := os.ReadFile(src)
			if err != nil {
				return err
			}
			return files.Write(dst, bytes.ToUpper(b), private)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 || result.Conflicts != 2 || result.LinksKept != 0 {
		t.Fatalf("result = %+v", result)
	}
	if !reflect.DeepEqual(calls, []string{"old/ready->sessions/ready"}) {
		t.Fatalf("codec calls = %v", calls)
	}
	if !reflect.DeepEqual(result.LiveSkipped, []string{"sessions/live"}) {
		t.Fatalf("live skipped = %v", result.LiveSkipped)
	}
	count, err := files.Prune(target, []string{"config"}, result.Written, result.Protected)
	if err != nil || count != 1 {
		t.Fatalf("prune = %d, %v", count, err)
	}
	// Only vendor-selected authoritative directories prune.
	if body(t, target, "sessions/live") != "newer" || body(t, target, "sessions/ready") != "ENCODED" || body(t, target, "config/collision/local") != "keep directory contents" || body(t, target, "unmanaged/local") != "keep" || body(t, target, "unmanaged/parent") != "local parent file" {
		t.Fatal("restore/prune changed protected contents")
	}
}

func TestRestoreSymlinksAndAliasedRoot(t *testing.T) {
	source, target, external := t.TempDir(), t.TempDir(), t.TempDir()
	externalFile := put(t, external, "local", "outside")
	link(t, filepath.Join(target, "leaf"), externalFile)
	link(t, filepath.Join(target, "..named"), external)
	put(t, source, "leaf", "stale")
	put(t, source, "..named/local", "stale")
	put(t, source, "ordinary", "new")
	alias := filepath.Join(t.TempDir(), "target")
	link(t, alias, target)
	result, err := files.Restore(files.RestoreOptions{SourceDir: source, TargetDir: alias, Write: copier})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 || result.LinksKept != 2 {
		t.Fatalf("result = %+v", result)
	}
	count, err := files.Prune(alias, []string{"..named"}, result.Written, result.Protected)
	if err != nil || count != 0 {
		t.Fatalf("prune = %d, %v", count, err)
	}
	if body(t, external, "local") != "outside" || body(t, target, "ordinary") != "new" {
		t.Fatal("symlink guards differ")
	}
}

func TestRestoreStopsOnCodecFailure(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	put(t, source, "a", "a")
	put(t, source, "b", "b")
	put(t, target, "local", "survive")
	want := errors.New("codec failed")
	calls := 0
	result, err := files.Restore(files.RestoreOptions{SourceDir: source, TargetDir: target, Write: func(string, string, string, string) error { calls++; return want }})
	if result != nil || !errors.Is(err, want) || calls != 1 {
		t.Fatalf("result = %+v, %v, calls %d", result, err, calls)
	}
	if body(t, target, "local") != "survive" {
		t.Fatal("failure pruned target")
	}
}

func TestRestoreRejectsEscapingDestination(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	put(t, source, "input", "bytes")
	for _, rel := range []string{"", ".", "../outside", filepath.Join(target, "absolute")} {
		t.Run(rel, func(t *testing.T) {
			_, err := files.Restore(files.RestoreOptions{SourceDir: source, TargetDir: target, Plan: func(string) (string, bool) { return rel, false }, Write: func(string, string, string, string) error { t.Fatal("invalid path reached codec"); return nil }})
			if err == nil {
				t.Fatal("expected invalid destination error")
			}
		})
	}
}
