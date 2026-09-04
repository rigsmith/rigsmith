package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/spf13/cobra"
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
	prevStore, prevApp, prevTTY := desktopStore, newDesktopApp, interactive
	desktopStore = func() (*desktop.Store, error) { return st, nil }
	newDesktopApp = func() desktop.App { return stubApp{open: map[string]bool{p.DataDir(): open}} }
	// Off a terminal, whatever `go test` is attached to: the destructive
	// tiers must fail with the --yes hint, never block on a prompt.
	interactive = func() bool { return false }
	t.Cleanup(func() { desktopStore, newDesktopApp, interactive = prevStore, prevApp, prevTTY })
	return st, p
}

// `desktop list` shows each profile's size, in the text and as sizeBytes in
// the JSON, so growth is discoverable without a separate command.
func TestDesktopList_ShowsSizes(t *testing.T) {
	_, p := pruneCommandFixture(t, false)
	size, err := desktop.DirSize(p.Dir())
	if err != nil || size == 0 {
		t.Fatalf("fixture size = %d, %v", size, err)
	}
	run := func(args ...string) string {
		t.Helper()
		var buf bytes.Buffer
		cmd := newDesktopListCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v\n%s", err, buf.String())
		}
		return buf.String()
	}
	if out := run(); !strings.Contains(out, desktop.HumanSize(size)) {
		t.Errorf("text output lacks the size %s:\n%s", desktop.HumanSize(size), out)
	}
	var got struct {
		Profiles []struct {
			Name        string `json:"name"`
			SizeBytes   int64  `json:"sizeBytes"`
			SizeUnknown bool   `json:"sizeUnknown"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(run("--json")), &got); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, row := range got.Profiles {
		if row.Name == "work" {
			found = true
			if row.SizeBytes != size || row.SizeUnknown {
				t.Errorf("work: sizeBytes=%d sizeUnknown=%v, want %d and known", row.SizeBytes, row.SizeUnknown, size)
			}
		}
	}
	if !found {
		t.Errorf("work missing from JSON: %+v", got)
	}
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

// With several profiles, an open one anywhere in the list stops the command
// before anything is deleted from the others. Profiles are visited in name
// order, so the open one is work, visited after personal: a single pass
// that pruned as it went would have emptied personal's cache first.
func TestDesktopPrune_OpenProfileStopsBeforeAnyDeletion(t *testing.T) {
	st, work := pruneCommandFixture(t, true)
	personal, err := st.Get("personal")
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(personal.DataDir(), "Cache", "data_0")
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runPrune(t); err == nil || !strings.Contains(err.Error(), "work is open") || !strings.Contains(err.Error(), "nothing was deleted") {
		t.Fatalf("err = %v, want a refusal that names the open profile", err)
	}
	if _, serr := os.Stat(cache); serr != nil {
		t.Error("personal's cache was deleted before the open profile after it was found")
	}
	if _, serr := os.Stat(filepath.Join(work.DataDir(), "Cache")); serr != nil {
		t.Error("the open profile's cache was deleted")
	}
}

// --vm and --all name different things to lose; both at once is refused
// rather than silently taking the larger.
func TestDesktopPrune_RefusesBothTiers(t *testing.T) {
	pruneCommandFixture(t, false)
	if _, err := runPrune(t, "--vm", "--all", "--yes"); err == nil || !strings.Contains(err.Error(), "vm") || !strings.Contains(err.Error(), "all") {
		t.Fatalf("err = %v, want a refusal naming both flags", err)
	}
}

// Tab completion offers profile names and emails, never file paths.
func TestDesktopPrune_CompletesProfiles(t *testing.T) {
	pruneCommandFixture(t, false)
	cmd := newDesktopPruneCmd()
	got, directive := cmd.ValidArgsFunction(cmd, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want no file completion", directive)
	}
	for _, want := range []string{"work", "work@example.com", "personal"} {
		if !slices.Contains(got, want) {
			t.Errorf("completions %v lack %q", got, want)
		}
	}
	if more, _ := cmd.ValidArgsFunction(cmd, []string{"work"}, ""); len(more) != 0 {
		t.Errorf("a second argument offered completions: %v", more)
	}
}

// The no-name form names a directory under the store it could not read as a
// profile instead of silently leaving it out of "every profile".
func TestDesktopPrune_NamesUnreadableProfiles(t *testing.T) {
	st, _ := pruneCommandFixture(t, false)
	if err := os.MkdirAll(filepath.Join(st.Root, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root, "broken", "profile.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runPrune(t, "--dry-run")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "broken") || !strings.Contains(out, "not a readable profile") {
		t.Errorf("unreadable profile not named:\n%s", out)
	}
}
