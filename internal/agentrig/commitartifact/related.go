package commitartifact

import (
	"bytes"
	"context"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

// ConflictSide identifies an immutable input tree, never the working directory.
type ConflictSide uint8

const (
	BaseSide ConflictSide = iota
	OurSide
	TheirSide
)

// RelatedFiles is valid only during a resolver call. Read accepts regular files
// in the selected side. Add proposes a regular 0644 file in the resulting tree;
// an existing file is accepted only when its bytes and mode are identical.
// Neither operation can replace a conflict path, follow symlinks or run filters.
// Reads/additions are bounded to 4 MiB each, 128 MiB total and 256 operations per
// merge. Ignored errors still abort recovery. Calls must be sequential.
type RelatedFiles interface {
	Read(context.Context, ConflictSide, string) ([]byte, error)
	Add(context.Context, string, []byte) error
	// SnapshotTime traces the current owner file on ours/theirs to its byte origin.
	// At most 512 commit/path visits and eight parents per commit per merge.
	SnapshotTime(context.Context, ConflictSide) (time.Time, error)
}

const relatedByteLimit = 4 << 20
const relatedTotalLimit = 128 << 20

type relatedFiles struct {
	repo         gitRepo
	tree         string
	sides        [3]string
	conflicts    []conflictStages
	owner        conflictStages
	updates      strings.Builder
	added        map[string]string
	budget       int64
	calls        int
	err          error
	origins      map[string]time.Time
	originVisits int
}

func newRelatedFiles(r gitRepo, tree, a, b string, conflicts []conflictStages) *relatedFiles {
	return &relatedFiles{repo: r, tree: tree, sides: [3]string{"", a, b}, conflicts: conflicts, added: map[string]string{}, budget: relatedTotalLimit}
}

// Exact literal lookup: paths never become revisions, options or pathspec magic.
func (r gitRepo) relatedEntry(ctx context.Context, tree, path string) (mode, oid string, err error) {
	if !objectID(tree) || !publicationPath(path) {
		return "", "", ErrInvalid
	}
	raw, err := r.run(ctx, nil, "ls-tree", "-z", "--full-tree", tree, "--", ":(literal)"+path)
	if err != nil || raw == "" {
		return "", "", err
	}
	header, name, ok := strings.Cut(raw, "\t")
	fields := strings.Fields(header)
	if !ok || name != path+"\x00" || len(fields) != 3 || !objectID(fields[2]) {
		return "", "", ErrInvalid
	}
	return fields[0], fields[2], nil
}

func (f *relatedFiles) begin(ctx context.Context) error {
	if f.err != nil {
		return f.err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f.calls++
	if f.calls > 256 {
		return artifact.ErrTooLarge
	}
	return nil
}

func (f *relatedFiles) Read(ctx context.Context, side ConflictSide, path string) (data []byte, err error) {
	defer func() {
		if err != nil {
			f.err = err
		}
	}()
	if err = f.begin(ctx); err != nil {
		return nil, err
	}
	if side > TheirSide || !publicationPath(path) {
		return nil, ErrInvalid
	}
	if side == BaseSide {
		if f.owner.oids[0] == "" {
			return nil, ErrConflict
		}
		if f.sides[0] == "" {
			raw, e := f.repo.run(ctx, nil, "merge-base", "--all", f.sides[1], f.sides[2])
			if e != nil {
				return nil, e
			}
			bases := strings.Fields(raw)
			// A virtual base has no single, provable companion tree.
			if len(bases) != 1 || !objectID(bases[0]) {
				return nil, ErrConflict
			}
			f.sides[0] = bases[0]
		}
		mode, oid, e := f.repo.relatedEntry(ctx, f.sides[0], f.owner.path)
		if e != nil {
			return nil, e
		}
		if mode != f.owner.mode || oid != f.owner.oids[0] {
			return nil, ErrConflict
		}
	}
	mode, oid, err := f.repo.relatedEntry(ctx, f.sides[side], path)
	if err != nil {
		return nil, err
	}
	if mode != "100644" && mode != "100755" {
		return nil, ErrConflict
	}
	var blob bytes.Buffer
	err = f.repo.runTo(ctx, nil, &boundedOutput{w: &blob, left: min(int64(relatedByteLimit), f.budget)}, "cat-file", "blob", oid)
	if err != nil {
		return nil, err
	}
	f.budget -= int64(blob.Len())
	return blob.Bytes(), nil
}

func pathsOverlap(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func (f *relatedFiles) Add(ctx context.Context, path string, data []byte) (err error) {
	defer func() {
		if err != nil {
			f.err = err
		}
	}()
	if err = f.begin(ctx); err != nil {
		return err
	}
	if !publicationPath(path) {
		return ErrInvalid
	}
	if len(data) > relatedByteLimit || int64(len(data)) > f.budget {
		return artifact.ErrTooLarge
	}
	f.budget -= int64(len(data))
	for _, c := range f.conflicts {
		if pathsOverlap(path, c.path) {
			return ErrConflict
		}
	}
	for prior := range f.added {
		if prior != path && pathsOverlap(path, prior) {
			return ErrConflict
		}
	}
	// update-index must not replace an existing directory or file ancestor.
	for prefix := path; ; {
		mode, _, e := f.repo.relatedEntry(ctx, f.tree, prefix)
		if e != nil {
			return e
		}
		if prefix == path {
			if mode != "" && mode != "100644" {
				return ErrConflict
			}
		} else if mode != "" && mode != "040000" {
			return ErrConflict
		}
		i := strings.LastIndexByte(prefix, '/')
		if i < 0 {
			break
		}
		prefix = prefix[:i]
	}
	oid, err := f.repo.run(ctx, bytes.NewReader(data), "hash-object", "-w", "--stdin")
	if err != nil {
		return err
	}
	oid = strings.TrimSpace(oid)
	if !objectID(oid) {
		return ErrInvalid
	}
	if prior, ok := f.added[path]; ok {
		if prior != oid {
			return ErrConflict
		}
		return nil
	}
	_, existing, err := f.repo.relatedEntry(ctx, f.tree, path)
	if err != nil {
		return err
	}
	if existing != "" && existing != oid {
		return ErrConflict
	}
	f.added[path] = oid
	f.updates.WriteString("100644 " + oid + "\t" + path + "\x00")
	return nil
}
