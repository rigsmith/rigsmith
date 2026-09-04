package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func pruneFixture(t *testing.T, withZst bool) (*Store, Profile) {
	t.Helper()
	st := NewStore(filepath.Join(t.TempDir(), "desktop"))
	p, err := st.Create("work", "work@example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	data := p.DataDir()
	write := func(rel string, n int) {
		path := filepath.Join(data, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Cache/data_0", 4096)
	write("Code Cache/js/index", 4096)
	write("Local Storage/leveldb/000001.log", 4096) // chat state: never reclaimable
	write("vm_bundles/claudevm.bundle/rootfs.img", 8192)
	write("vm_bundles/claudevm.bundle/vmlinuz", 4096)
	if withZst {
		write("vm_bundles/claudevm.bundle/rootfs.img.zst", 4096)
	}
	return st, p
}

func tierOf(u Usage, rel string) (PruneTier, bool) {
	for _, e := range u.Entries {
		if e.Rel == rel {
			return e.Tier, true
		}
	}
	return 0, false
}

func TestMeasure_ClassifiesTiers(t *testing.T) {
	_, p := pruneFixture(t, true)
	u, err := Measure(p)
	if err != nil {
		t.Fatal(err)
	}
	if u.Total == 0 {
		t.Fatal("total = 0, want the profile's size")
	}
	want := map[string]PruneTier{
		"Cache":                                 PruneCaches,
		"Code Cache":                            PruneCaches,
		"vm_bundles/claudevm.bundle/rootfs.img": PruneVM,
		"vm_bundles":                            PruneAll,
	}
	for rel, tier := range want {
		got, ok := tierOf(u, rel)
		if !ok || got != tier {
			t.Errorf("%s: tier %v (present=%v), want %v", rel, got, ok, tier)
		}
	}
	if _, ok := tierOf(u, "Local Storage"); ok {
		t.Error("Local Storage classified as reclaimable — that is the chat state")
	}
	if u.Reclaimable(PruneCaches) >= u.Reclaimable(PruneVM) || u.Reclaimable(PruneVM) >= u.Reclaimable(PruneAll) {
		t.Errorf("tiers should nest: caches=%d vm=%d all=%d",
			u.Reclaimable(PruneCaches), u.Reclaimable(PruneVM), u.Reclaimable(PruneAll))
	}
	if u.Reclaimable(PruneAll) > u.Total {
		t.Errorf("reclaimable %d exceeds total %d", u.Reclaimable(PruneAll), u.Total)
	}
}

// Without the compressed image beside it, dropping rootfs.img forces a download
// rather than a re-extract, so it must cost the --all tier, not --vm.
func TestMeasure_RootfsWithoutZstNeedsAll(t *testing.T) {
	_, p := pruneFixture(t, false)
	u, err := Measure(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tierOf(u, "vm_bundles/claudevm.bundle/rootfs.img"); ok {
		t.Error("rootfs.img offered at --vm tier with no rootfs.img.zst to re-extract from")
	}
	if u.Reclaimable(PruneVM) != u.Reclaimable(PruneCaches) {
		t.Error("--vm should reclaim nothing beyond caches when the image cannot be re-extracted")
	}
	if tier, ok := tierOf(u, "vm_bundles"); !ok || tier != PruneAll {
		t.Errorf("vm_bundles tier = %v (present=%v), want all", tier, ok)
	}
}

func TestPrune_RespectsTier(t *testing.T) {
	_, p := pruneFixture(t, true)
	data := p.DataDir()
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(data, filepath.FromSlash(rel)))
		return err == nil
	}

	removed, err := Prune(p, PruneCaches)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("caches tier removed %d entries, want 2: %+v", len(removed), removed)
	}
	if exists("Cache") || exists("Code Cache") {
		t.Error("caches still present after prune")
	}
	if !exists("vm_bundles/claudevm.bundle/rootfs.img") || !exists("Local Storage") {
		t.Error("caches tier touched more than the caches")
	}

	if _, err := Prune(p, PruneVM); err != nil {
		t.Fatal(err)
	}
	if exists("vm_bundles/claudevm.bundle/rootfs.img") {
		t.Error("rootfs.img survived --vm")
	}
	if !exists("vm_bundles/claudevm.bundle/rootfs.img.zst") {
		t.Error("--vm removed the compressed image it needs to re-extract from")
	}

	if _, err := Prune(p, PruneAll); err != nil {
		t.Fatal(err)
	}
	if exists("vm_bundles") {
		t.Error("vm_bundles survived --all")
	}
	if !exists("Local Storage") {
		t.Error("--all removed chat state")
	}
	if _, err := os.Stat(filepath.Join(p.Dir(), "profile.json")); err != nil {
		t.Error("profile.json removed — prune must leave the profile registered")
	}
}

func TestDirSize_MissingIsZero(t *testing.T) {
	n, err := DirSize(filepath.Join(t.TempDir(), "nope"))
	if err != nil || n != 0 {
		t.Fatalf("DirSize(missing) = %d, %v; want 0, nil", n, err)
	}
}

func TestHumanSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 8 << 30: "8.0 GB"} {
		if got := HumanSize(n); got != want {
			t.Errorf("HumanSize(%d) = %q, want %q", n, got, want)
		}
	}
}
