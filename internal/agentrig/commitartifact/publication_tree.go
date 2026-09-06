package commitartifact

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

// boundedOutput also bounds Git's listing output before parsing or allocating
// per-entry metadata. Blob contents are streamed directly into private files.
type boundedOutput struct {
	w        io.Writer
	left     int64
	exceeded bool
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	if int64(len(p)) > w.left {
		w.exceeded = true
		return 0, artifact.ErrTooLarge
	}
	n, err := w.w.Write(p)
	w.left -= int64(n)
	return n, err
}

func (r gitRepo) checkTree(ctx context.Context, commit, work string, limit int64, checks ...func(context.Context, string) error) error {
	if limit == 0 {
		limit = artifact.DefaultMaxBytes
	}
	root, err := os.MkdirTemp(work, "tree-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	var listing bytes.Buffer
	if err = r.runTo(ctx, nil, &boundedOutput{w: &listing, left: 64 << 20}, "ls-tree", "-rltz", "--full-tree", commit); err != nil {
		return err
	}
	tree, err := parsePublicationTree(listing.Bytes(), limit, publicationMetadataLimit)
	if err != nil {
		return err
	}
	if err := tree.root.createDirs(ctx, root); err != nil {
		return err
	}
	if err := r.materializeBlobs(ctx, root, tree.files); err != nil {
		return err
	}
	for _, check := range checks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := check(ctx, root); err != nil {
			return err
		}
	}
	// Verify raw blob hashes and exact directory membership in-process. This
	// catches policy mutations without starting hash-object once per file.
	return tree.root.verify(ctx, root)
}

func publicationPath(path string) bool {
	// Git paths use slash separators on every host. Do not use filepath.IsLocal
	// here: its Windows device-name rules depend on the worker's OS/version.
	if len(path) > 4096 || !utf8.ValidString(path) || strings.ContainsAny(path, "\\:<>\"|?*") {
		return false
	}
	for _, ch := range path {
		if ch < 32 {
			return false
		}
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || len(part) > 255 || part == "." || part == ".." || strings.EqualFold(part, ".git") || strings.TrimRight(part, ". ") != part || publicationDeviceName(part) {
			return false
		}
	}
	return true
}

func publicationDeviceName(part string) bool {
	// Reserve device names even with extensions or spaces before an extension.
	// Some Windows versions accept more spellings; use one conservative policy
	// for all workers and for every component, including parent directories.
	stem, _, _ := strings.Cut(part, ".")
	stem = strings.ToUpper(strings.TrimRight(stem, " "))
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT") {
		switch stem[3:] {
		case "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³":
			return true
		}
	}
	return false
}
