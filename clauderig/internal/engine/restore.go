package engine

import (
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/manifest"
	"github.com/rigsmith/clauderig/internal/project"
	"github.com/rigsmith/clauderig/internal/redact"
	"github.com/rigsmith/core/pathmap"
)

// RestoreRootResult summarises one root's restore.
type RestoreRootResult struct {
	ID             string
	Files          int
	SlugsRewritten int
	Pruned         int // files removed as deleted-upstream (--prune)
	Skipped        bool
}

// prunableDirs are the authoritative config dirs where "deleted upstream" means
// "remove locally". projects/ is deliberately excluded — it's additive (a machine
// accumulates its own local sessions), so it is never pruned.
var prunableDirs = []string{"skills", "commands", "agents", "plans"}

// RestoreReport is the outcome of a restore.
type RestoreReport struct {
	Roots []RestoreRootResult
}

// RestoreOptions configure a restore.
type RestoreOptions struct {
	StagingDir string
	Config     *config.Config
	Machine    config.Machine
	Manifest   *manifest.Manifest
	// TargetOverride maps a root id to an absolute target dir, overriding its
	// resolved location (used by `restore --dir` to write into a test folder).
	TargetOverride map[string]string
	// OverriddenOnly restores only roots present in TargetOverride (so a --dir
	// restore touches the test folder and nothing else).
	OverriddenOnly bool
	// Prune removes files under the authoritative config dirs (prunableDirs) that
	// aren't in the synced set — so a skill deleted upstream is deleted locally.
	// Never touches projects/ (additive).
	Prune bool
}

// Restore writes the staged file set back to this machine's roots, rewriting CLI
// project slugs for this machine's path layout (via the manifest) and merging
// redacted config so the machine's real secrets are never clobbered by a
// placeholder. Caller handles target-non-empty safety (backup/abort) first.
func Restore(opts RestoreOptions) (*RestoreReport, error) {
	rep := &RestoreReport{}
	for _, r := range opts.Config.Roots {
		if !r.Enabled {
			continue
		}
		rr := RestoreRootResult{ID: r.ID}
		override, hasOverride := opts.TargetOverride[r.ID]
		if opts.OverriddenOnly && !hasOverride {
			continue // --dir mode: only the overridden root(s)
		}
		target, st := override, pathmap.StatusResolved
		if !hasOverride {
			target, st = opts.Config.RootLocation(r.ID, opts.Machine)
		}
		stageRoot := filepath.Join(opts.StagingDir, r.ID)
		if st != pathmap.StatusResolved || !dirExists(stageRoot) {
			rr.Skipped = true
			rep.Roots = append(rep.Roots, rr)
			continue
		}

		var slugMap map[string]string
		if r.ID == "cli" && opts.Manifest != nil {
			slugMap = buildSlugMap(opts.Manifest, opts.Machine)
		}
		rewritten := map[string]bool{}
		written := map[string]bool{}

		files, err := listFiles(stageRoot)
		if err != nil {
			return nil, err
		}
		for _, rel := range files {
			targetRel := rel
			if r.ID == "cli" && strings.HasPrefix(rel, "projects/") {
				newRel, srcSlug, did := rewriteProjectRel(rel, slugMap)
				targetRel = newRel
				if did {
					rewritten[srcSlug] = true
				}
			}
			src := filepath.Join(stageRoot, filepath.FromSlash(rel))
			dst := filepath.Join(target, filepath.FromSlash(targetRel))

			if strings.HasSuffix(rel, ".json") {
				if err := restoreJSON(src, dst, opts.Machine.Resolver()); err != nil {
					return nil, err
				}
			} else if err := copyFile(src, dst); err != nil {
				return nil, err
			}
			written[targetRel] = true
			rr.Files++
		}
		rr.SlugsRewritten = len(rewritten)

		if opts.Prune && r.ID == "cli" {
			pruned, err := pruneConfigDirs(target, written)
			if err != nil {
				return nil, err
			}
			rr.Pruned = pruned
		}
		rep.Roots = append(rep.Roots, rr)
	}
	return rep, nil
}

// pruneConfigDirs removes files under the authoritative config dirs that aren't in
// the restored set (deleted upstream). written holds the slash-relative paths just
// written. projects/ is never visited.
func pruneConfigDirs(target string, written map[string]bool) (int, error) {
	pruned := 0
	for _, dir := range prunableDirs {
		base := filepath.Join(target, dir)
		if !dirExists(base) {
			continue
		}
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, rerr := filepath.Rel(target, p)
			if rerr != nil {
				return rerr
			}
			if !written[filepath.ToSlash(rel)] {
				if err := os.Remove(p); err != nil {
					return err
				}
				pruned++
			}
			return nil
		})
		if err != nil {
			return pruned, err
		}
	}
	return pruned, nil
}

// buildSlugMap maps each source slug to this machine's slug, via the manifest's
// portable template resolved for this machine. A project with no template (cwd not
// under a known folder) or an unresolvable one keeps its source slug.
func buildSlugMap(m *manifest.Manifest, mc config.Machine) map[string]string {
	out := make(map[string]string, len(m.Projects))
	res := mc.Resolver()
	for srcSlug, p := range m.Projects {
		if p.Template == "" {
			out[srcSlug] = srcSlug
			continue
		}
		ns, _, st := project.RewriteFromTemplate(p.Template, res)
		if st == pathmap.StatusResolved {
			out[srcSlug] = ns
		} else {
			out[srcSlug] = srcSlug
		}
	}
	return out
}

// rewriteProjectRel maps "projects/<srcSlug>/<rest>" to the target slug. It
// returns the new rel, the source slug, and whether the slug actually changed.
func rewriteProjectRel(rel string, slugMap map[string]string) (newRel, srcSlug string, rewrote bool) {
	parts := strings.SplitN(rel, "/", 3)
	if len(parts) < 2 {
		return rel, "", false
	}
	srcSlug = parts[1]
	tgt, ok := slugMap[srcSlug]
	if !ok || tgt == srcSlug {
		return rel, srcSlug, false
	}
	newRel = "projects/" + tgt
	if len(parts) == 3 {
		newRel += "/" + parts[2]
	}
	return newRel, srcSlug, true
}

// restoreJSON writes a synced JSON file to dst, resolving portable path values to
// this machine and merging onto the local file so the machine's real secrets
// survive (any synced JSON may carry redaction placeholders). Unparseable JSON
// falls back to a raw copy.
func restoreJSON(src, dst string, resolver *pathmap.Resolver) error {
	synced, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(synced, &v); err != nil {
		return copyBytes(dst, synced) // not JSON after all — copy raw
	}
	v, _ = pathmap.ResolveJSONValues(v, resolver)
	resolved, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return copyBytes(dst, synced)
	}
	resolved = append(resolved, '\n')

	local, _ := os.ReadFile(dst) // absent on a fresh machine
	merged, err := redact.MergeBytes(resolved, local)
	if err != nil {
		return writeFile(dst, resolved)
	}
	return writeFile(dst, merged)
}

func listFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyBytes(dst string, data []byte) error { return writeFile(dst, data) }
