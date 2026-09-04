package desktop

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// PruneTier is how much of a profile `desktop prune` may reclaim. Each tier
// includes the ones below it. The tiers are ordered by what is lost: nothing,
// then whatever was created inside the Cowork VM, then a download.
type PruneTier int

const (
	// PruneCaches reclaims Electron's regenerable caches only. Nothing is lost.
	PruneCaches PruneTier = iota
	// PruneVM also drops the unpacked Cowork VM root filesystem, which Desktop
	// re-extracts from the compressed image beside it on next launch. Anything
	// created inside the VM and never exported to the host is lost.
	PruneVM
	// PruneAll also drops the compressed image, kernel and initramfs — the whole
	// VM bundle — accepting a re-download on next launch.
	PruneAll
)

func (t PruneTier) String() string {
	switch t {
	case PruneCaches:
		return "caches"
	case PruneVM:
		return "vm"
	case PruneAll:
		return "all"
	}
	return fmt.Sprintf("tier(%d)", int(t))
}

// electronCaches are the directories Chromium/Electron rebuild from nothing.
// Top-level under the profile's data dir; each is safe to delete while the app
// is closed. DawnCache is the older name of the two Dawn* directories.
var electronCaches = []string{"Cache", "Code Cache", "GPUCache", "DawnWebGPUCache", "DawnGraphiteCache", "DawnCache"}

// vmBundlesDir is where Desktop keeps the Cowork local-agent VM, one bundle
// directory per image (claudevm.bundle today). Inside, rootfs.img is the
// unpacked root filesystem — sparse, provisioned at ~10 GB, and it only ever
// grows, because deleting files inside the VM never shrinks it on the host.
// rootfs.img.zst beside it is the pristine compressed image it was unpacked
// from.
const (
	vmBundlesDir = "vm_bundles"
	vmRootfs     = "rootfs.img"
	vmRootfsZst  = "rootfs.img.zst"
)

// PruneEntry is one reclaimable path in a profile.
type PruneEntry struct {
	// Rel is the path relative to the profile's data directory.
	Rel string
	// Size is the space the path takes ON DISK — allocated blocks, not the
	// logical length — so a sparse VM image reports what it actually costs.
	Size int64
	// Tier is the lowest prune tier that reclaims this entry.
	Tier PruneTier
	// Note says what happens after it is gone, when that is not obvious.
	Note string
}

// Usage is what a profile costs and what prune could give back.
type Usage struct {
	// Total is the whole profile directory, on disk.
	Total int64
	// Entries are the reclaimable paths, cheapest tier first.
	Entries []PruneEntry
}

// Reclaimable is the space the given tier would free.
func (u Usage) Reclaimable(tier PruneTier) int64 {
	var n int64
	for _, e := range u.Entries {
		if e.Tier <= tier {
			n += e.Size
		}
	}
	return n
}

// Measure sizes a profile and classifies what prune could reclaim from it. It
// reads nothing but metadata, so a 10 GB profile costs a directory walk, not a
// read. A profile that does not exist yet measures as empty.
func Measure(p Profile) (Usage, error) {
	total, err := DirSize(p.Dir())
	if err != nil {
		return Usage{}, err
	}
	u := Usage{Total: total}
	data := p.DataDir()
	for _, name := range electronCaches {
		dir := filepath.Join(data, name)
		if fi, serr := os.Stat(dir); serr != nil || !fi.IsDir() {
			continue
		}
		size, serr := DirSize(dir)
		if serr != nil {
			return Usage{}, serr
		}
		u.Entries = append(u.Entries, PruneEntry{Rel: name, Size: size, Tier: PruneCaches, Note: "regenerated as needed"})
	}

	bundles := filepath.Join(data, vmBundlesDir)
	entries, rerr := os.ReadDir(bundles)
	if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return Usage{}, rerr
	}
	var vmTier int64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		img := filepath.Join(bundles, e.Name(), vmRootfs)
		fi, serr := os.Lstat(img)
		if serr != nil || !fi.Mode().IsRegular() {
			continue
		}
		size := diskUsage(img, fi)
		rel := filepath.ToSlash(filepath.Join(vmBundlesDir, e.Name(), vmRootfs))
		// The unpacked image is only a --vm reclaim when the compressed one is
		// still beside it: that is what Desktop re-extracts from. Without it the
		// image is as expensive to replace as the whole bundle, so it costs the
		// same tier.
		if _, zerr := os.Stat(filepath.Join(bundles, e.Name(), vmRootfsZst)); zerr == nil {
			u.Entries = append(u.Entries, PruneEntry{Rel: rel, Size: size, Tier: PruneVM,
				Note: "VM resets to pristine; re-extracted from " + vmRootfsZst + " on next launch"})
			vmTier += size
		}
	}
	if len(entries) > 0 {
		size, serr := DirSize(bundles)
		if serr != nil {
			return Usage{}, serr
		}
		if rest := size - vmTier; rest > 0 {
			u.Entries = append(u.Entries, PruneEntry{Rel: vmBundlesDir, Size: rest, Tier: PruneAll,
				Note: "compressed image, kernel and initramfs; re-downloaded on next launch"})
		}
	}
	sort.SliceStable(u.Entries, func(i, j int) bool { return u.Entries[i].Tier < u.Entries[j].Tier })
	return u, nil
}

// Prune removes every reclaimable entry at or below tier and returns what it
// removed. The caller must ensure the profile's window is closed first — a live
// Electron instance writes into these directories, and the VM image may be
// mounted by a running Cowork agent.
//
// Removal goes cheapest-first, so a failure part-way leaves the profile with
// the most valuable data still intact. The entries removed before the failure
// are returned alongside the error.
func Prune(p Profile, tier PruneTier) ([]PruneEntry, error) {
	u, err := Measure(p)
	if err != nil {
		return nil, err
	}
	var done []PruneEntry
	for _, e := range u.Entries {
		if e.Tier > tier {
			continue
		}
		if rerr := os.RemoveAll(filepath.Join(p.DataDir(), filepath.FromSlash(e.Rel))); rerr != nil {
			return done, fmt.Errorf("removing %s: %w", e.Rel, rerr)
		}
		done = append(done, e)
	}
	return done, nil
}

// DirSize is the on-disk size of a directory tree: allocated blocks where the
// platform reports them, so sparse files count what they cost rather than what
// they claim. Symlinks are not followed. A missing directory is zero, not an
// error — a profile that has never been opened has no data dir yet.
func DirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if errors.Is(werr, fs.ErrNotExist) {
				return nil
			}
			return werr
		}
		if d.IsDir() {
			return nil
		}
		fi, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		total += diskUsage(path, fi)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	return total, err
}

// HumanSize renders a byte count in the largest unit that keeps it under 1024.
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
