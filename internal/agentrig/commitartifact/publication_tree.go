package commitartifact

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

// boundedOutput also bounds Git's listing output before parsing or allocating
// per-entry metadata. Blob contents are streamed directly into private files.
type boundedOutput struct {
	w    io.Writer
	left int64
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	if int64(len(p)) > w.left {
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
	if err = r.runTo(ctx, nil, &boundedOutput{&listing, 64 << 20}, "ls-tree", "-rlz", "--full-tree", commit); err != nil {
		return err
	}
	modes := map[string]os.FileMode{}
	spellings := map[string]string{}
	entries := bytes.Split(bytes.TrimSuffix(listing.Bytes(), []byte{0}), []byte{0})
	if len(entries) > 1000000 {
		return artifact.ErrTooLarge
	}
	for _, entry := range entries {
		if len(entry) == 0 {
			continue
		}
		header, path, ok := strings.Cut(string(entry), "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) != 4 || fields[1] != "blob" || !objectID(fields[2]) || !publicationPath(path) {
			return ErrInvalid
		}
		mode := os.FileMode(0600)
		switch fields[0] {
		case "100644":
		case "100755":
			mode = 0700
		default:
			return ErrInvalid
		}
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil || size < 0 {
			return ErrInvalid
		}
		if size > limit {
			return artifact.ErrTooLarge
		}
		limit -= size
		// Refuse collisions on every supported host, including case-folded parent
		// directories. Never audit one spelling and publish another hidden spelling.
		prefix := ""
		for _, part := range strings.Split(path, "/") {
			if prefix != "" {
				prefix += "/"
			}
			prefix += part
			folded := strings.ToLower(prefix)
			if old, exists := spellings[folded]; exists && old != prefix {
				return ErrInvalid
			}
			spellings[folded] = prefix
		}
		dest := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		output := &boundedOutput{f, size}
		err = r.runTo(ctx, nil, output, "cat-file", "blob", fields[2])
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if output.left != 0 {
			return ErrInvalid
		}
		modes[path] = mode
	}
	for _, check := range checks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := check(ctx, root); err != nil {
			return err
		}
	}
	// Callbacks are policies, not permission to clean a working copy while an
	// unaudited original tree is sent to the remote.
	tree, err := r.writeTree(ctx, root, root, modes)
	if err != nil {
		return err
	}
	original, err := r.run(ctx, nil, "rev-parse", commit+"^{tree}")
	if err != nil {
		return err
	}
	if tree != strings.TrimSpace(original) {
		return fmt.Errorf("publication policy modified the inspected tree: %w", ErrInvalid)
	}
	return nil
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
