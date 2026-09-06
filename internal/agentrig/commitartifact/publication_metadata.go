package commitartifact

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

const publicationMetadataLimit int64 = 64 << 20

type publicationFile struct {
	path, oid string
	size      int64
	mode      os.FileMode
}

// Each directory stores only immediate component names. Deep paths no longer
// retain every cumulative prefix. The budget charges names, file descriptors,
// slice capacity and conservative map/node overhead before retaining metadata.
type publicationNode struct {
	name      string
	children  map[string]*publicationNode
	file      *publicationFile
	directory bool
}
type publicationTree struct {
	root   publicationNode
	files  []*publicationFile
	budget int64
}

func (t *publicationTree) charge(n int64) error {
	if n > t.budget {
		return artifact.ErrTooLarge
	}
	t.budget -= n
	return nil
}
func (t *publicationTree) add(path string, file *publicationFile) error {
	if file != nil {
		if err := t.charge(int64(256 + len(file.path) + len(file.oid))); err != nil {
			return err
		}
	}
	node := &t.root
	for _, part := range strings.Split(path, "/") {
		if node.file != nil {
			return ErrInvalid
		}
		folded := strings.ToLower(part)
		child := node.children[folded]
		if child != nil {
			if child.name != part {
				return ErrInvalid
			}
		} else {
			if err := t.charge(int64(512 + len(part) + len(folded))); err != nil {
				return err
			}
			if node.children == nil {
				node.children = make(map[string]*publicationNode)
			}
			child = &publicationNode{name: strings.Clone(part)}
			node.children[strings.Clone(folded)] = child
		}
		node = child
	}
	if file == nil {
		if node.file != nil || node.directory {
			return ErrInvalid
		}
		node.directory = true
		return nil
	}
	if node.file != nil || node.directory || len(node.children) != 0 {
		return ErrInvalid
	}
	node.file = file
	t.files = append(t.files, file)
	return nil
}

func parsePublicationTree(listing []byte, byteLimit, metadataLimit int64) (*publicationTree, error) {
	tree := &publicationTree{budget: metadataLimit}
	for len(listing) > 0 {
		entry, rest, ok := bytes.Cut(listing, []byte{0})
		if !ok || len(entry) == 0 {
			return nil, ErrInvalid
		}
		listing = rest
		if len(tree.files) >= 1000000 {
			return nil, artifact.ErrTooLarge
		}
		header, path, ok := strings.Cut(string(entry), "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) != 4 || !objectID(fields[2]) || !publicationPath(path) {
			return nil, ErrInvalid
		}
		if fields[1] == "tree" && fields[0] == "040000" && fields[3] == "-" {
			if err := tree.add(path, nil); err != nil {
				return nil, err
			}
			continue
		}
		if fields[1] != "blob" {
			return nil, ErrInvalid
		}
		mode := os.FileMode(0600)
		switch fields[0] {
		case "100644":
		case "100755":
			mode = 0700
		default:
			return nil, ErrInvalid
		}
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil || size < 0 {
			return nil, ErrInvalid
		}
		if size > byteLimit {
			return nil, artifact.ErrTooLarge
		}
		byteLimit -= size
		if err := tree.add(path, &publicationFile{path: path, oid: fields[2], size: size, mode: mode}); err != nil {
			return nil, err
		}
	}
	return tree, nil
}

// verify checks original blob IDs rather than host permissions: Git executable
// modes stay attached to the candidate even on hosts that cannot represent them.
// Extra/missing directories and files, links, devices and changed bytes all fail.
func (n *publicationNode) verify(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if n.file != nil {
		if !info.Mode().IsRegular() || info.Size() != n.file.size {
			return ErrInvalid
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		actual, err := f.Stat()
		if err != nil || !os.SameFile(info, actual) {
			f.Close()
			return ErrInvalid
		}
		var h hash.Hash = sha1.New()
		if len(n.file.oid) == 64 {
			h = sha256.New()
		}
		fmt.Fprintf(h, "blob %d%c", n.file.size, 0)
		read, err := io.Copy(h, io.LimitReader(publicationReader{ctx, f}, n.file.size+1))
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if read != n.file.size || hex.EncodeToString(h.Sum(nil)) != n.file.oid {
			return ErrInvalid
		}
		return nil
	}
	if !info.IsDir() {
		return ErrInvalid
	}
	// Read in bounded batches rather than allocating another entire directory.
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	seen := 0
	for {
		entries, err := dir.ReadDir(128)
		if err != nil && err != io.EOF {
			dir.Close()
			return err
		}
		for _, entry := range entries {
			child := n.children[strings.ToLower(entry.Name())]
			if child == nil || child.name != entry.Name() {
				dir.Close()
				return ErrInvalid
			}
			seen++
		}
		if err == io.EOF {
			break
		}
	}
	if err := dir.Close(); err != nil {
		return err
	}
	if seen != len(n.children) {
		return ErrInvalid
	}
	// Release the directory handle before descending so deep valid paths do
	// not consume one file descriptor per ancestor.
	for _, child := range n.children {
		if err := child.verify(ctx, filepath.Join(path, child.name)); err != nil {
			return err
		}
	}
	return nil
}

type publicationReader struct {
	ctx context.Context
	r   io.Reader
}

func (r publicationReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func (n *publicationNode) createDirs(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, child := range n.children {
		if child.file != nil {
			continue
		}
		dest := filepath.Join(path, child.name)
		if err := os.Mkdir(dest, 0700); err != nil {
			return err
		}
		if err := child.createDirs(ctx, dest); err != nil {
			return err
		}
	}
	return nil
}
