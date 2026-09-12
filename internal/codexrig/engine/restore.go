package engine

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/agentrig/redact"
	"github.com/rigsmith/rigsmith/internal/codexrig/codec"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/manifest"
	"github.com/rigsmith/rigsmith/internal/codexrig/rollout"
)

// RestoreOptions configure a restore.
type RestoreOptions struct {
	StagingDir string
	Config     *config.Config
	Machine    config.Machine
	Manifest   *manifest.Manifest

	// Prune removes local files under the pruneable directories that are no
	// longer in the repo — a skill or a prompt deleted on another machine.
	// Off unless asked: "it is not in the backup" and "you just wrote it" look
	// identical from here.
	Prune bool

	// TargetOverride maps a root id to an absolute directory, used verbatim.
	// What `restore --dir` uses to unpack a snapshot somewhere harmless.
	TargetOverride map[string]string
}

// RootRestore summarises one root's restore.
type RootRestore struct {
	ID       string
	Written  int
	Merged   int // structured files whose local secrets or additions were preserved
	Kept     int // rollouts already present, left alone
	Skipped  int
	Conflict int // a directory stands where a file should go
	Links    int // a path the user had symlinked, written through rather than replaced
	Pruned   int
	Absent   bool
}

// RestoreReport is the outcome of a restore.
type RestoreReport struct {
	Roots []RootRestore
	// CommentsLost names the configuration files whose content genuinely
	// changed and which therefore lost their comments, because TOML does not
	// survive a decode/encode round trip with them. Named rather than counted:
	// the answer to "which of my files did you reformat" has to be a list.
	CommentsLost []string
}

// Written totals the files this restore wrote.
func (r RestoreReport) Written() int {
	n := 0
	for _, rr := range r.Roots {
		n += rr.Written
	}
	return n
}

// pruneableDirs are the places a restore may delete from. Narrow on purpose:
// everything outside them is either machine state or something Codex owns, and a
// backup tool that deletes from a live home needs a very short list of where.
var pruneableDirs = []string{"skills", "prompts", "rules", "themes"}

// Restore writes the staged snapshot back onto this machine.
func Restore(opts RestoreOptions) (*RestoreReport, error) {
	if opts.Config == nil {
		return nil, errors.New("restore needs a config")
	}
	rep := &RestoreReport{}
	resolver := opts.Machine.Resolver()

	for _, r := range opts.Config.Roots {
		if !r.Enabled {
			continue
		}
		rr := RootRestore{ID: r.ID}
		stageRoot := filepath.Join(opts.StagingDir, r.ID)
		if !dirExists(stageRoot) {
			rr.Absent = true
			rep.Roots = append(rep.Roots, rr)
			continue
		}
		target, ok := opts.TargetOverride[r.ID]
		if !ok {
			var status pathmap.Status
			target, status = r.ResolveOn(opts.Machine)
			if status != pathmap.StatusResolved {
				rr.Absent = true
				rep.Roots = append(rep.Roots, rr)
				continue
			}
		}
		if err := os.MkdirAll(target, 0o700); err != nil {
			return nil, err
		}

		files, err := listStagedFiles(stageRoot)
		if err != nil {
			return nil, err
		}
		written := map[string]bool{}

		for _, rel := range files {
			src := filepath.Join(stageRoot, filepath.FromSlash(rel))
			dst := filepath.Join(target, filepath.FromSlash(rel))
			written[rel] = true

			// A rollout that is already here is never overwritten. It is
			// append-only, so the local copy is at least as complete as the
			// staged one — and it may be the file a live Codex session is
			// writing into right now, which is the one file a restore must not
			// touch. Restoring a session is bringing back one this machine does
			// not have.
			if rollout.IsRolloutRel(rel) {
				if _, err := os.Lstat(dst); err == nil {
					rr.Kept++
					continue
				}
			}

			// A directory where a file belongs, or a file standing in for a
			// parent directory. Report it and move on; forcing past it means
			// deleting whatever is there.
			if conflicted(target, dst) {
				rr.Conflict++
				continue
			}
			// Somebody pointed this path at a managed location. Writing through
			// the link honours that; replacing it discards a deliberate choice.
			if throughLink(dst) {
				rr.Links++
			}

			if c, ok := codec.For(rel); ok {
				merged, lostComments, err := restoreStructured(c, src, dst, resolver)
				if err != nil {
					rr.Skipped++
					continue
				}
				if merged {
					rr.Merged++
				}
				if lostComments {
					rep.CommentsLost = append(rep.CommentsLost, filepath.Join(r.ID, rel))
				}
				rr.Written++
				continue
			}

			mode := os.FileMode(0o644)
			if strings.HasSuffix(rel, ".json") || filepath.Base(rel) == "auth.json" {
				mode = 0o600
			}
			if err := copyOut(src, dst, mode); err != nil {
				rr.Skipped++
				continue
			}
			rr.Written++
		}

		if opts.Prune {
			pruned, err := pruneMissing(target, written)
			if err != nil {
				return nil, err
			}
			rr.Pruned = pruned
		}
		rep.Roots = append(rep.Roots, rr)
	}
	sort.Strings(rep.CommentsLost)
	return rep, nil
}

// restoreStructured merges the staged copy of a configuration file onto the
// local one, preserving this machine's secrets and its own additions.
//
// The order is the whole point. The staged copy has portable path templates and
// redaction sentinels in it; resolving the templates makes it this machine's
// paths, and merging over the LOCAL file makes every sentinel fall back to the
// value this machine already had. A field that was redacted and has no local
// counterpart is dropped entirely rather than written as the literal sentinel,
// because Codex would then send the sentinel as a credential.
func restoreStructured(c codec.Codec, src, dst string, resolver *pathmap.Resolver) (merged, lostComments bool, err error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return false, false, err
	}
	v, err := c.Decode(data)
	if err != nil {
		return false, false, err
	}
	v, _ = pathmap.ResolveJSONValues(v, resolver)
	v, _ = ResolveKeys(v, resolver)

	localRaw, lerr := os.ReadFile(dst)
	var local any
	if lerr == nil {
		if parsed, perr := c.Decode(localRaw); perr == nil {
			local = parsed
			merged = true
		}
	}
	result := redact.Merge(v, local)

	out, err := c.Encode(result)
	if err != nil {
		return merged, false, err
	}

	if lerr == nil {
		// Nothing changed semantically: leave the file exactly as it is. This
		// is what lets a machine whose config did not change upstream keep its
		// comments, which a decode/encode round trip would otherwise drop. It
		// also means a restore over an unchanged machine writes nothing at all.
		if local != nil && reflect.DeepEqual(local, result) {
			return merged, false, nil
		}
		if bytes.Equal(localRaw, out) {
			return merged, false, nil
		}
		// The content genuinely moved, so the file is rewritten and its
		// comments do not survive. Reported rather than silent.
		if c.Name() == "toml" && bytes.Contains(localRaw, []byte("#")) {
			lostComments = true
		}
	}
	if err := writeFileMode(dst, out, 0o600); err != nil {
		return merged, lostComments, err
	}
	return merged, lostComments, nil
}

// conflicted reports whether something at or above dst makes writing a file
// there impossible.
func conflicted(root, dst string) bool {
	if st, err := os.Lstat(dst); err == nil && st.IsDir() {
		return true
	}
	// A FILE standing where an ancestor directory needs to be. Checked upward
	// because the leaf is not where this usually shows up.
	dir := filepath.Dir(dst)
	for len(dir) > len(root) {
		if st, err := os.Lstat(dir); err == nil && !st.IsDir() {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return false
}

// throughLink reports whether dst, or a directory above it, is a symlink.
func throughLink(dst string) bool {
	p := dst
	for i := 0; i < 32; i++ {
		st, err := os.Lstat(p)
		if err == nil && st.Mode()&os.ModeSymlink != 0 {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
	return false
}

// pruneMissing removes local files under the pruneable directories that the
// snapshot does not contain.
func pruneMissing(target string, written map[string]bool) (int, error) {
	pruned := 0
	for _, dir := range pruneableDirs {
		base := filepath.Join(target, dir)
		if !dirExists(base) {
			continue
		}
		var doomed []string
		err := filepath.Walk(base, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if fi.IsDir() {
				return nil
			}
			rel, rerr := filepath.Rel(target, p)
			if rerr != nil {
				return rerr
			}
			rel = filepath.ToSlash(rel)
			// Codex's own system skills are excluded from the backup, so their
			// absence from it is not evidence that anybody deleted them.
			if strings.HasPrefix(rel, "skills/.system") {
				return nil
			}
			if !written[rel] {
				doomed = append(doomed, p)
			}
			return nil
		})
		if err != nil {
			return pruned, err
		}
		for _, p := range doomed {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return pruned, err
			}
			pruned++
		}
		pruneEmptyDirs(base)
	}
	return pruned, nil
}

func copyOut(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFileMode(dst, data, mode)
}

func writeFileMode(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Write THROUGH a symlink rather than replacing it: a path somebody pointed
	// elsewhere was pointed there deliberately.
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	if err := os.WriteFile(target, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
