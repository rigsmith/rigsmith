// Package since maps the files changed since a git ref (see
// gitutil.ChangedFilesSince) to the packages and changesets they belong to.
// Ported from net-changesets' Shared/SinceChanges.cs, minus the interop
// extensions — the only changeset extension here is .md.
package since

import (
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// ChangedProjectNames returns the names of the packages that own at least one
// of the changed files (paths are absolute; package dirs are resolved against
// repoRoot).
func ChangedProjectNames(changedFiles []string, pkgs []plugin.Package, repoRoot string) []string {
	var names []string
	seen := map[string]bool{}
	for _, p := range pkgs {
		dir := filepath.Join(repoRoot, p.Dir)
		for _, f := range changedFiles {
			if isUnder(f, dir) {
				if !seen[p.Name] {
					seen[p.Name] = true
					names = append(names, p.Name)
				}
				break
			}
		}
	}
	return names
}

// AnyChangesetAdded reports whether any changed file is a changeset: a
// non-README .md file inside the changeset directory.
func AnyChangesetAdded(changedFiles []string, changesetDir string) bool {
	return len(ChangedChangesetIDs(changedFiles, changesetDir)) > 0
}

// ChangedChangesetIDs returns the ids (file name without extension) of the
// changesets among the changed files — the changesets added since the ref.
func ChangedChangesetIDs(changedFiles []string, changesetDir string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, f := range changedFiles {
		if !isUnder(f, changesetDir) {
			continue
		}
		name := filepath.Base(f)
		if strings.EqualFold(name, "README.md") || !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		id := strings.TrimSuffix(name, filepath.Ext(name))
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func isUnder(file, dir string) bool {
	dir = filepath.Clean(dir)
	return file == dir || strings.HasPrefix(file, dir+string(filepath.Separator))
}
