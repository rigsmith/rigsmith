// Package commitartifact retains audited Git snapshots as self-contained bundles
// inside the shared durable artifact format. Publication uses an explicit
// transport; canonical refs, indexes, checkouts and queue markers stay untouched.
package commitartifact

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

// RefName is the explicit source ref to import from a retained bundle.
const RefName = "refs/rig/capture"

var ErrInvalid = errors.New("invalid retained commit")

// Request supplies immutable commit policy and a verified capture reference.
// PolicyID must change when Prepare/Audit behavior changes. Seeded captures must
// name a retained seed in their metadata. Stores must be disjoint and outside
// the canonical repository and vendor source roots.
type Request struct {
	Captures, Commits                          artifact.Store
	CaptureRef                                 string
	PolicyID, Message, AuthorName, AuthorEmail string
	Time                                       time.Time
	Prepare, Audit                             func(context.Context, string) error
}

// Commit identifies the exact snapshot and its retained ancestry. BundlePath is
// set by Open and belongs to the caller's extracted destination.
type Commit struct {
	CaptureRef, Commit, Tree, Parent string
	BundlePath                       string `json:"-"`
}

func objectID(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && (len(b) == 20 || len(b) == 32) && s == strings.ToLower(s)
}

func requestKey(r Request) (string, error) {
	if r.Prepare == nil || r.Audit == nil || strings.TrimSpace(r.PolicyID) == "" ||
		strings.TrimSpace(r.Message) == "" || len(r.Message) > 4096 || strings.ContainsRune(r.Message, 0) ||
		strings.TrimSpace(r.AuthorName) == "" || strings.TrimSpace(r.AuthorEmail) == "" ||
		strings.ContainsAny(r.AuthorName+r.AuthorEmail, "\r\n<>\x00") ||
		r.Time.IsZero() || r.Time.Unix() < 0 {
		return "", ErrInvalid
	}
	identity, err := json.Marshal(struct {
		Version, Capture, Policy, Message, Name, Email string
		Timestamp                                      int64
	}{"git-bundle-v1", r.CaptureRef, r.PolicyID, r.Message, r.AuthorName, r.AuthorEmail, r.Time.Unix()})
	if err != nil {
		return "", err
	}
	return artifact.Key(identity), nil
}

// Build returns only after the self-contained bundle has been durably sealed.
// Same-request retries verify and reflush the existing output without loading
// capture bytes or parent objects again. A corrupt output is never rebuilt.
// A missing capture or retained seed fails closed before the first build. Git
// objects are never loaded from a mutable canonical repository here.
func Build(ctx context.Context, r Request) (string, error) {
	key, err := requestKey(r)
	if err != nil {
		return "", err
	}
	return r.Commits.BuildWithMetadata(ctx, key, func(ctx context.Context, output string, meta *artifact.Metadata) error {
		work := filepath.Dir(output)
		tree := filepath.Join(work, "snapshot")
		extracted, err := r.Captures.ExtractWithMetadata(ctx, r.CaptureRef, tree)
		if err != nil {
			return err
		}
		parent := extracted.Metadata.BaseReference
		if (parent != "" && (!objectID(parent) || extracted.Metadata.SeedReference == "")) || (parent == "" && extracted.Metadata.SeedReference != "") {
			return ErrInvalid
		}
		if err = r.Prepare(ctx, tree); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = r.Audit(ctx, tree); err != nil {
			return err
		}
		repo, err := initRepo(ctx, filepath.Join(work, "git"), parent)
		if err != nil {
			return err
		}
		if parent != "" {
			if err = repo.loadSeed(ctx, r.Captures, extracted.Metadata.SeedReference, parent, filepath.Join(work, "seed")); err != nil {
				return err
			}
		}

		treeID, err := repo.writeTree(ctx, tree, tree, extracted.Modes)
		if err != nil {
			return err
		}
		args := []string{"commit-tree", treeID}
		if parent != "" {
			args = append(args, "-p", parent)
		}
		repo.identity = []string{
			"GIT_AUTHOR_NAME=" + r.AuthorName, "GIT_AUTHOR_EMAIL=" + r.AuthorEmail,
			"GIT_COMMITTER_NAME=" + r.AuthorName, "GIT_COMMITTER_EMAIL=" + r.AuthorEmail,
			fmt.Sprintf("GIT_AUTHOR_DATE=@%d +0000", r.Time.Unix()),
			fmt.Sprintf("GIT_COMMITTER_DATE=@%d +0000", r.Time.Unix()),
		}
		sha, err := repo.run(ctx, strings.NewReader(r.Message+"\n"), args...)
		if err != nil {
			return err
		}
		sha = strings.TrimSpace(sha)
		if !objectID(sha) {
			return ErrInvalid
		}
		if _, err = repo.run(ctx, nil, "update-ref", RefName, sha); err != nil {
			return err
		}
		bundle := filepath.Join(output, "commit.bundle")
		if _, err = repo.run(ctx, nil, "bundle", "create", bundle, RefName); err != nil {
			return err
		}
		// A fresh empty repository verifies there are no external prerequisites.
		check, err := initRepo(ctx, filepath.Join(work, "verify"), sha)
		if err != nil {
			return err
		}
		if _, err = check.run(ctx, nil, "bundle", "verify", bundle); err != nil {
			return err
		}
		info := Commit{CaptureRef: r.CaptureRef, Commit: sha, Tree: treeID, Parent: parent}
		data, err := json.Marshal(info)
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(output, "commit.json"), data, 0600); err != nil {
			return err
		}
		meta.BaseReference = sha
		return nil
	})
}

// Open verifies and extracts a retained commit into a NEW destination. The
// caller owns that directory after success. It validates the bundle in an empty
// temporary repository, including its full ancestry and recorded tree/parent.
// This read does not acknowledge an uncertain Build; retry Build for that.
func Open(ctx context.Context, store artifact.Store, ref, dest string) (result Commit, err error) {
	extracted, err := store.ExtractWithMetadata(ctx, ref, dest)
	if err != nil {
		return result, err
	}
	meta := extracted.Metadata
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dest)
		}
	}()
	entries, err := os.ReadDir(dest)
	if err != nil {
		return result, err
	}
	if len(entries) != 2 {
		return result, ErrInvalid
	}
	f, err := os.Open(filepath.Join(dest, "commit.json"))
	if err != nil {
		return result, err
	}
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	closeErr := f.Close()
	if err != nil {
		return result, err
	}
	if closeErr != nil {
		return result, closeErr
	}
	if len(data) > 16384 {
		return result, ErrInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&result); err != nil {
		return result, err
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		return result, ErrInvalid
	}
	if !objectID(result.Commit) || !objectID(result.Tree) ||
		(result.Parent != "" && !objectID(result.Parent)) || meta.BaseReference != result.Commit {
		return result, ErrInvalid
	}
	result.BundlePath = filepath.Join(dest, "commit.bundle")
	work, err := os.MkdirTemp(filepath.Dir(dest), ".commit-verify-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(work)
	repo, err := initRepo(ctx, filepath.Join(work, "git"), result.Commit)
	if err != nil {
		return result, err
	}
	if _, err = repo.run(ctx, nil, "bundle", "verify", result.BundlePath); err != nil {
		return result, err
	}
	if _, err = repo.run(ctx, nil, "fetch", "--no-tags", "--no-write-fetch-head", "--", result.BundlePath, RefName+":"+RefName); err != nil {
		return result, err
	}
	got, err := repo.run(ctx, nil, "show", "--no-patch", "--format=%H%n%T%n%P", RefName)
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(got) != strings.TrimSpace(result.Commit+"\n"+result.Tree+"\n"+result.Parent) {
		return result, ErrInvalid
	}
	if err = repo.checkObjects(ctx); err != nil {
		return result, err
	}
	return result, nil
}

// All Git writes use a private bare repository with no user configuration,
// inherited Git environment, templates or hooks. Raw blobs bypass attributes,
// filters, ignore rules and line-ending conversions entirely.
type gitRepo struct {
	dir      string
	identity []string
}

func initRepo(ctx context.Context, dir, oid string) (gitRepo, error) {
	r := gitRepo{dir: dir}
	if err := os.Mkdir(dir, 0700); err != nil {
		return r, err
	}
	format := "sha1"
	if len(oid) == 64 {
		format = "sha256"
	}
	_, err := r.run(ctx, nil, "init", "--bare", "--template=", "--object-format="+format, "--initial-branch=main", ".")
	return r, err
}

// Control-command stdout is bounded, including conflict diagnostics on failure.
// Large blob/tree streams use runTo with their own explicit bounds or consumers.
const gitOutputLimit int64 = 1 << 20

func (r gitRepo) run(ctx context.Context, input io.Reader, args ...string) (string, error) {
	var out bytes.Buffer
	err := r.runTo(ctx, input, &boundedOutput{w: &out, left: gitOutputLimit}, args...)
	if err != nil {
		return "", err // Never expose partial output as a usable object ID.
	}
	return out.String(), nil
}

func (r gitRepo) runTo(ctx context.Context, input io.Reader, output io.Writer, args ...string) error {
	cmd := r.command(ctx, args...)
	cmd.Stdin, cmd.Stdout = input, output
	err := cmd.Run()
	if bounded, ok := output.(*boundedOutput); ok && bounded.exceeded {
		// Wait can prefer the child's broken-pipe exit over the writer error.
		// Preserve the capacity failure after the child and copy goroutine exit.
		err = artifact.ErrTooLarge
	}
	if err != nil {
		// Do not surface raw Git diagnostics or local paths in queue failure codes.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("retained commit git %s: %w", args[0], err)
	}
	return nil
}

func (r gitRepo) checkObjects(ctx context.Context) error {
	// Dangling retry candidates are expected. Keep strict integrity checks,
	// suppress their notices, and never buffer diagnostics we do not consume.
	return r.runTo(ctx, nil, io.Discard, "fsck", "--strict", "--no-reflogs", "--no-dangling", "--no-progress")
}

// command applies the same private Git isolation to one-shot and streaming calls.
func (r gitRepo) command(ctx context.Context, args ...string) *exec.Cmd {
	flags := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.attributesFile=" + os.DevNull, "-c", "gc.auto=0", "-c", "maintenance.auto=false", "-c", "commit.gpgsign=false", "-c", "protocol.allow=never", "-c", "protocol.file.allow=always"}
	cmd := exec.CommandContext(ctx, "git", append(flags, args...)...)
	cmd.Dir = r.dir
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(entry), "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_ATTR_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_ALLOW_PROTOCOL=file")
	cmd.Env = append(cmd.Env, r.identity...)
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func (r gitRepo) writeTree(ctx context.Context, root, dir string, modes map[string]os.FileMode) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var tree bytes.Buffer
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), ".git") {
			return "", ErrInvalid
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		mode, kind := "100644", "blob"
		var sha string
		switch {
		case info.IsDir():
			mode, kind = "040000", "tree"
			sha, err = r.writeTree(ctx, root, path, modes)
		case info.Mode().IsRegular():
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return "", err
			}
			fileMode, archived := modes[filepath.ToSlash(rel)]
			if !archived {
				fileMode = info.Mode()
			} // Newly prepared vendor metadata.
			if fileMode&0100 != 0 {
				mode = "100755"
			}
			var f *os.File
			f, err = os.Open(path)
			if err == nil {
				sha, err = r.run(ctx, f, "hash-object", "-w", "--no-filters", "--stdin")
				closeErr := f.Close()
				if err == nil {
					err = closeErr
				}
			}
		default:
			return "", ErrInvalid
		}
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&tree, "%s %s %s\t%s%c", mode, kind, strings.TrimSpace(sha), entry.Name(), 0)
	}
	sha, err := r.run(ctx, &tree, "mktree", "-z")
	return strings.TrimSpace(sha), err
}
