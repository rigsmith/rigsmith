package commitartifact

import (
	"bytes"
	"context"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

// ResolveConflict handles bounded, regular-file content/add-add conflicts only.
// The path and raw side blobs are detached; nil Base means an add/add conflict.
// Return ErrConflict to decline. The resolver cannot edit the private repository.
// Its output is reinserted as a raw blob and the whole candidate is validated and
// audited before publication. No resolver is called for delete/edit or mode changes.
type ResolveConflict func(ctx context.Context, path string, base, ours, theirs []byte) ([]byte, error)

const conflictByteLimit = 1 << 20
const conflictTotalLimit = 16 << 20

type conflictStages struct {
	path, mode string
	oids       [3]string
}

func parseConflicts(raw string) (string, []conflictStages, error) {
	tree, rest, ok := strings.Cut(raw, "\x00")
	if !ok || !objectID(tree) {
		return "", nil, ErrInvalid
	}
	var conflicts []conflictStages
	seen := map[string]bool{}
	for rest != "" {
		record, tail, ok := strings.Cut(rest, "\x00")
		if !ok || record == "" {
			return "", nil, ErrInvalid
		}
		rest = tail
		header, path, ok := strings.Cut(record, "\t")
		fields := strings.Fields(header)
		if !ok || !publicationPath(path) || len(fields) != 3 || !objectID(fields[1]) ||
			(fields[0] != "100644" && fields[0] != "100755") || len(fields[2]) != 1 || fields[2][0] < '1' || fields[2][0] > '3' {
			return "", nil, ErrConflict
		}
		if len(conflicts) == 0 || conflicts[len(conflicts)-1].path != path {
			if seen[strings.ToLower(path)] {
				return "", nil, ErrConflict
			}
			if len(conflicts) >= 128 {
				return "", nil, artifact.ErrTooLarge
			}
			seen[strings.ToLower(path)] = true
			conflicts = append(conflicts, conflictStages{path: path, mode: fields[0]})
		}
		c := &conflicts[len(conflicts)-1]
		stage := int(fields[2][0] - '1')
		if c.mode != fields[0] || c.oids[stage] != "" {
			return "", nil, ErrConflict
		}
		c.oids[stage] = fields[1]
	}
	if len(conflicts) == 0 {
		return "", nil, ErrConflict
	}
	for _, c := range conflicts {
		if c.oids[1] == "" || c.oids[2] == "" {
			return "", nil, ErrConflict
		}
	}
	return tree, conflicts, nil
}

func (r gitRepo) resolveConflicts(ctx context.Context, raw string, resolve ResolveConflict) (string, error) {
	tree, conflicts, err := parseConflicts(raw)
	if err != nil {
		return "", err
	}
	var updates strings.Builder
	budget := int64(conflictTotalLimit)
	for _, c := range conflicts {
		var sides [3][]byte
		for i, oid := range c.oids {
			if oid == "" {
				continue
			}
			var blob bytes.Buffer
			limit := min(int64(conflictByteLimit), budget)
			if err := r.runTo(ctx, nil, &boundedOutput{w: &blob, left: limit}, "cat-file", "blob", oid); err != nil {
				return "", err
			}
			sides[i] = blob.Bytes()
			if sides[i] == nil {
				sides[i] = []byte{}
			}
			budget -= int64(len(sides[i]))
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		resolved, err := resolve(ctx, c.path, sides[0], sides[1], sides[2])
		if err != nil {
			return "", err
		}
		if len(resolved) > conflictByteLimit || int64(len(resolved)) > budget {
			return "", artifact.ErrTooLarge
		}
		budget -= int64(len(resolved))
		oid, err := r.run(ctx, bytes.NewReader(resolved), "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		oid = strings.TrimSpace(oid)
		if !objectID(oid) {
			return "", ErrInvalid
		}
		updates.WriteString(c.mode + " " + oid + "\t" + c.path + "\x00")
	}
	// The index belongs to this private bare repository. No checkout, filters or
	// filesystem interpretation of conflict paths is involved.
	if _, err := r.run(ctx, nil, "read-tree", tree); err != nil {
		return "", err
	}
	if _, err := r.run(ctx, strings.NewReader(updates.String()), "update-index", "-z", "--index-info"); err != nil {
		return "", err
	}
	return r.run(ctx, nil, "write-tree")
}

func (r gitRepo) mergeWithPolicy(ctx context.Context, a, b, message string, resolve ResolveConflict) (string, error) {
	if resolve == nil {
		return r.merge(ctx, a, b, message)
	}
	if yes, err := r.ancestor(ctx, b, a); err != nil || yes {
		return a, err
	}
	if yes, err := r.ancestor(ctx, a, b); err != nil || yes {
		return b, err
	}
	var out bytes.Buffer
	// Disable rename inference for this limited policy. Unsupported structural
	// conflicts must not disappear merely because a content resolver accepts a path.
	err := r.runTo(ctx, nil, &boundedOutput{w: &out, left: gitOutputLimit},
		"-c", "merge.renames=false", "merge-tree", "--write-tree", "-z", "--no-messages", a, b)
	var tree string
	if err == nil {
		tree = strings.TrimSuffix(out.String(), "\x00")
	} else if ctx.Err() == nil && gitExited(err, 1) {
		tree, err = r.resolveConflicts(ctx, out.String(), resolve)
	}
	if err != nil {
		return "", err
	}
	tree = strings.TrimSpace(tree)
	if !objectID(tree) {
		return "", ErrInvalid
	}
	sha, err := r.run(ctx, strings.NewReader(message+"\n"), "commit-tree", tree, "-p", a, "-p", b)
	return strings.TrimSpace(sha), err
}
