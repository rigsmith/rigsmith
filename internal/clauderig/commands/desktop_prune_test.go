package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
)

func pruneCommandFixture(t *testing.T, open bool) (*desktop.Store, desktop.Profile) {
	t.Helper()
	st := targetStore(t)
	p, err := st.Get("work")
	if err != nil {
		t.Fatal(err)
	}
	for rel, n := range map[string]int{
		"Cache/data_0":                              4096,
		"vm_bundles/claudevm.bundle/rootfs.img":     8192,
		"vm_bundles/claudevm.bundle/rootfs.img.zst": 4096,
		"Local Storage/leveldb/000001.log":          4096,
	} {
		path := filepath.Join(p.DataDir(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prevStore, prevApp := desktopStore, newDesktopApp
	desktopStore = func() (*desktop.Store, error) { return st, nil }
	newDesktopApp = func() desktop.App { return stubApp{open: map[string]bool{p.DataDir(): open}} }
	t.Cleanup(func() { desktopStore, newDesktopApp = prevStore, prevApp })
	return st, p
}

func runPrune(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd := newDesktopPruneCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestDesktopPrune_DryRunDeletesNothing(t *testing.T) {
	_, p := pruneCommandFixture(t, false)
	out, err := runPrune(t, "work", "--dry-run", "--all")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Cache", "rootfs.img", "vm_bundles", "dry run"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, out)
		}
	}
	if _, serr := os.Stat(filepath.Join(p.DataDir(), "Cache")); serr != nil {
		t.Error("dry run deleted the cache")
	}
}

func TestDesktopPrune_DefaultTierTakesCachesOnly(t *testing.T) {
	_, p := pruneCommandFixture(t, false)
	out, err := runPrune(t, "work")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, serr := os.Stat(filepath.Join(p.DataDir(), "Cache")); serr == nil {
		t.Error("cache still present")
	}
	if _, serr := os.Stat(filepath.Join(p.DataDir(), "vm_bundles", "claudevm.bundle", "rootfs.img")); serr != nil {
		t.Error("default tier removed the VM image")
	}
	if !strings.Contains(out, "reclaimed") {
		t.Errorf("no reclaimed line:\n%s", out)
	}
}

// --vm loses data, so off a terminal it needs --yes; with it, only rootfs.img
// goes and the compressed image stays to re-extract from.
func TestDesktopPrune_VMNeedsYesOffATerminal(t *testing.T) {
	_, p := pruneCommandFixture(t, false)
	if _, err := runPrune(t, "work", "--vm"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("--vm without --yes: err = %v, want a --yes hint", err)
	}
	if _, serr := os.Stat(filepath.Join(p.DataDir(), "vm_bundles", "claudevm.bundle", "rootfs.img")); serr != nil {
		t.Fatal("refused prune still deleted the VM image")
	}
	if out, err := runPrune(t, "work", "--vm", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	bundle := filepath.Join(p.DataDir(), "vm_bundles", "claudevm.bundle")
	if _, serr := os.Stat(filepath.Join(bundle, "rootfs.img")); serr == nil {
		t.Error("rootfs.img survived --vm --yes")
	}
	if _, serr := os.Stat(filepath.Join(bundle, "rootfs.img.zst")); serr != nil {
		t.Error("--vm removed the compressed image")
	}
	if _, serr := os.Stat(filepath.Join(p.DataDir(), "Local Storage")); serr != nil {
		t.Error("--vm removed chat state")
	}
}

func TestDesktopPrune_RefusesAnOpenProfile(t *testing.T) {
	_, p := pruneCommandFixture(t, true)
	_, err := runPrune(t, "work")
	if err == nil || !strings.Contains(err.Error(), "is open") {
		t.Fatalf("open profile: err = %v, want a refusal", err)
	}
	if _, serr := os.Stat(filepath.Join(p.DataDir(), "Cache")); serr != nil {
		t.Error("refused prune still deleted the cache")
	}
}

func TestDesktopPrune_UnknownProfile(t *testing.T) {
	pruneCommandFixture(t, false)
	if _, err := runPrune(t, "nope"); err == nil || !strings.Contains(err.Error(), `no Desktop profile "nope"`) {
		t.Fatalf("unknown profile: err = %v", err)
	}
}
