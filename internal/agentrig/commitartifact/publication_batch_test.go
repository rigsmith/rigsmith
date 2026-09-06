package commitartifact

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

func TestPublicationMetadataBoundsDeepPathsAndCaseCollisions(t *testing.T) {
	oid := strings.Repeat("a", 40)
	var listing strings.Builder
	for i := range 100 {
		path := fmt.Sprintf("root-%d/", i) + strings.Repeat("a/", 1500) + "file"
		fmt.Fprintf(&listing, "100644 blob %s 0\t%s%c", oid, path, 0)
	}
	// A sub-megabyte listing can represent hundreds of thousands of components.
	// Capacity must be enforced while retaining metadata, before creating files.
	if _, err := parsePublicationTree([]byte(listing.String()), 0, 1<<20); !errors.Is(err, artifact.ErrTooLarge) {
		t.Fatalf("deep metadata did not exhaust bounded budget: %v", err)
	}
	for _, paths := range [][]string{{"Root/a", "root/b"}, {"a", "a/b"}, {"a/b", "a"}, {"a", "a"}} {
		var entries strings.Builder
		for _, path := range paths {
			fmt.Fprintf(&entries, "100644 blob %s 0\t%s%c", oid, path, 0)
		}
		if _, err := parsePublicationTree([]byte(entries.String()), 0, publicationMetadataLimit); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted collision %v: %v", paths, err)
		}
	}
	one := []byte(fmt.Sprintf("100644 blob %s 2\ta%c100644 blob %s 2\tb%c", oid, 0, oid, 0))
	if _, err := parsePublicationTree(one, 3, publicationMetadataLimit); !errors.Is(err, artifact.ErrTooLarge) {
		t.Fatal("lost aggregate blob limit", err)
	}
	if _, err := parsePublicationTree(one, 4, 0); !errors.Is(err, artifact.ErrTooLarge) {
		t.Fatal("lost metadata limit", err)
	}
}

func TestPublicationBatchRejectsMalformedStreams(t *testing.T) {
	oid := strings.Repeat("a", 40)
	file := &publicationFile{path: "file", oid: oid, size: 3, mode: 0600}
	good := oid + " blob 3\nabc\n"
	cases := map[string]string{
		"wrong-object": strings.Repeat("b", 40) + " blob 3\nabc\n",
		"wrong-type":   oid + " tree 3\nabc\n", "wrong-size": oid + " blob 4\nabc\n",
		"missing": oid + " missing\n", "short-body": oid + " blob 3\nab",
		"bad-delimiter": oid + " blob 3\nabcX", "extra-output": good + "unexpected",
		"long-header": strings.Repeat("a", 8192) + "\n",
	}
	for name, stream := range cases {
		t.Run(name, func(t *testing.T) {
			if err := materializeBatch(t.Context(), strings.NewReader(stream), t.TempDir(), []*publicationFile{file}); err == nil {
				t.Fatal("accepted malformed batch")
			}
		})
	}
	root := t.TempDir()
	if err := materializeBatch(t.Context(), strings.NewReader(good), root, []*publicationFile{file}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "file")); err != nil || string(b) != "abc" {
		t.Fatalf("%q %v", b, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := materializeBatch(ctx, strings.NewReader(good), t.TempDir(), []*publicationFile{file}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPublicationAuditUsesOneBlobBatchAndNoPerFileGitProcesses(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			repo, parent := seedRepository(t, format)
			payload := strings.Repeat("binary\x00bytes\r\n", 4096)
			oid := mustRun(t, repo, payload, "hash-object", "-w", "--stdin")
			var listing strings.Builder
			for i := range 256 {
				fmt.Fprintf(&listing, "100644 blob %s\tfile-%04d%c", oid, i, 0)
			}
			tree := mustRun(t, repo, listing.String(), "mktree", "-z")
			sha := mustRun(t, repo, "many files\n", "commit-tree", tree, "-p", parent)
			trace := filepath.Join(t.TempDir(), "git-trace.json")
			repo.identity = append(repo.identity, "GIT_TRACE2_EVENT="+trace)
			if err := repo.checkTree(t.Context(), sha, t.TempDir(), 0, func(_ context.Context, root string) error {
				got, err := os.ReadFile(filepath.Join(root, "file-0255"))
				if err != nil {
					return err
				}
				if string(got) != payload {
					t.Fatal("batch corrupted raw bytes")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(trace)
			if err != nil {
				t.Fatal(err)
			}
			starts, batches := 0, 0
			for _, line := range bytes.Split(data, []byte{'\n'}) {
				if len(line) == 0 {
					continue
				}
				var event struct {
					Event string   `json:"event"`
					Argv  []string `json:"argv"`
				}
				if err := json.Unmarshal(line, &event); err != nil {
					t.Fatal(err)
				}
				if event.Event == "start" {
					starts++
					if strings.Contains(strings.Join(event.Argv, " "), "cat-file --batch") {
						batches++
					}
				}
			}
			if starts != 2 || batches != 1 {
				t.Fatalf("%d processes / %d blob batches; want ls-tree plus one batch", starts, batches)
			}
		})
	}
}

func TestPublicationVerificationRejectsPolicyChangesAndPreservesEmptyTrees(t *testing.T) {
	repo, parent := seedRepository(t, "sha1")
	blob := mustRun(t, repo, "abc", "hash-object", "-w", "--stdin")
	empty := mustRun(t, repo, "", "mktree")
	tree := mustRun(t, repo, "040000 tree "+empty+"\tempty\x00"+"100644 blob "+blob+"\tfile\x00", "mktree", "-z")
	sha := mustRun(t, repo, "empty directory\n", "commit-tree", tree, "-p", parent)
	if err := repo.checkTree(t.Context(), sha, t.TempDir(), 0, func(_ context.Context, root string) error {
		info, err := os.Stat(filepath.Join(root, "empty"))
		if err != nil {
			return err
		}
		if !info.IsDir() {
			t.Fatal("lost empty tree")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"same-size-edit", "added-file", "added-directory", "removed-directory", "removed-file"} {
		t.Run(mode, func(t *testing.T) {
			err := repo.checkTree(t.Context(), sha, t.TempDir(), 0, func(_ context.Context, root string) error {
				switch mode {
				case "same-size-edit":
					return os.WriteFile(filepath.Join(root, "file"), []byte("xyz"), 0600)
				case "added-file":
					return os.WriteFile(filepath.Join(root, "extra"), nil, 0600)
				case "added-directory":
					return os.Mkdir(filepath.Join(root, "extra"), 0700)
				case "removed-directory":
					return os.Remove(filepath.Join(root, "empty"))
				default:
					return os.Remove(filepath.Join(root, "file"))
				}
			})
			if err == nil {
				t.Fatal("accepted changed policy tree")
			}
		})
	}
	// A missing Git object fails the batch protocol and returns after child cleanup.
	err := repo.materializeBlobs(t.Context(), t.TempDir(), []*publicationFile{{path: "missing", oid: strings.Repeat("a", 40), size: 0, mode: 0600}})
	if !errors.Is(err, ErrInvalid) && !errors.Is(err, io.EOF) {
		t.Fatalf("missing blob: %v", err)
	}
}

type cancelBatchRead struct {
	io.Reader
	cancel context.CancelFunc
	reads  int
}

func (r *cancelBatchRead) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.reads++
	if r.reads == 2 {
		r.cancel()
	}
	return n, err
}
func TestPublicationBatchStopsDuringBlobRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	oid := strings.Repeat("a", 40)
	body := strings.Repeat("x", 16384)
	input := &cancelBatchRead{Reader: strings.NewReader(fmt.Sprintf("%s blob %d\n%s\n", oid, len(body), body)), cancel: cancel}
	err := materializeBatch(ctx, input, t.TempDir(), []*publicationFile{{path: "file", oid: oid, size: int64(len(body)), mode: 0600}})
	if !errors.Is(err, context.Canceled) || input.reads < 2 {
		t.Fatalf("did not cancel inside the blob: reads=%d err=%v", input.reads, err)
	}
}
