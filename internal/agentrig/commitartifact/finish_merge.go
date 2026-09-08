package commitartifact

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MergeFinishPolicy validates the exact staged merge and both parent tips before
// recording it. Callers hold the canonical staging lease throughout this call.
// It never stages files, resolves conflicts, invokes hooks or changes worktree
// bytes. Unresolved or ambiguous operations fail closed for deliberate recovery.
type MergeFinishPolicy struct {
	Message, AuthorName, AuthorEmail string
	Time                             time.Time
	MaxTreeBytes                     int64
	Validate, Audit                  func(context.Context, string) error
}

// FinishStagedMerge completes a two-parent merge whose resolutions are already
// in the index. Other operations remain blocked. The original index and unstaged
// files are untouched. A compare-and-swap records the audited commit before Git
// forgets the merge; an interrupted cleanup is recognized by its exact parents,
// ORIG_HEAD and staged tree on retry. A settled checkout simply returns its HEAD.
func FinishStagedMerge(ctx context.Context, dir string, p MergeFinishPolicy) (string, error) {
	if !filepath.IsAbs(dir) || p.MaxTreeBytes < 0 {
		return "", ErrInvalid
	}
	if _, err := requestKey(Request{PolicyID: "finish-staged-merge-v1", Message: p.Message,
		AuthorName: p.AuthorName, AuthorEmail: p.AuthorEmail, Time: p.Time,
		Prepare: p.Validate, Audit: p.Audit}); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		return SettledHead(ctx, dir)
	} else if err != nil {
		return "", err
	}
	r := gitRepo{dir: dir}
	// Do not discard an autostash or interfere with another operation.
	for _, name := range []string{"CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer", "MERGE_AUTOSTASH", "BISECT_START"} {
		path, err := r.operationPath(ctx, name)
		if err != nil {
			return "", err
		}
		if _, err := os.Lstat(path); err == nil {
			return "", ErrConflict
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	mergePath, err := r.operationPath(ctx, "MERGE_HEAD")
	if err != nil {
		return "", err
	}
	merge, err := readMergeFile(mergePath, 256)
	if os.IsNotExist(err) {
		return SettledHead(ctx, dir)
	}
	if err != nil {
		return "", err
	}
	incoming := strings.TrimSuffix(string(merge), "\n")
	if !objectID(incoming) {
		return "", ErrConflict // Includes octopus merges and malformed state.
	}
	origPath, err := r.operationPath(ctx, "ORIG_HEAD")
	if err != nil {
		return "", err
	}
	orig, err := readMergeFile(origPath, 256)
	if err != nil {
		return "", fmt.Errorf("%w: original merge head unavailable", ErrConflict)
	}
	original := strings.TrimSuffix(string(orig), "\n")
	if !objectID(original) || incoming == original {
		return "", ErrConflict
	}
	head, err := r.run(ctx, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	head = strings.TrimSpace(head)
	if !objectID(head) {
		return "", ErrInvalid
	}
	indexPath, err := r.operationPath(ctx, "index")
	if err != nil {
		return "", err
	}
	index, err := readMergeFile(indexPath, 64<<20)
	if err != nil {
		return "", err
	}
	work, err := os.MkdirTemp("", "rig-staged-merge-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	privateIndex := filepath.Join(work, "index")
	if err := os.WriteFile(privateIndex, index, 0600); err != nil {
		return "", err
	}
	indexed := r
	indexed.identity = []string{"GIT_INDEX_FILE=" + privateIndex}
	unmerged, err := indexed.run(ctx, nil, "ls-files", "--unmerged")
	if err != nil {
		return "", err
	}
	if unmerged != "" {
		return "", ErrConflict
	}
	tree, err := indexed.run(ctx, nil, "write-tree")
	if err != nil {
		return "", err
	}
	tree = strings.TrimSpace(tree)
	if !objectID(tree) {
		return "", ErrInvalid
	}
	if err := r.completeHistory(ctx); err != nil {
		return "", err
	}
	if head != original {
		// Only resume cleanup of exactly this completed merge. Never infer
		// completion from ancestry alone or silently discard a changed index.
		actual, err := r.run(ctx, nil, "show", "-s", "--format=%T%n%P", head)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(actual) != tree+"\n"+original+" "+incoming {
			return "", ErrConflict
		}
	} else {
		if contains, err := r.ancestor(ctx, incoming, original); err != nil {
			return "", err
		} else if contains {
			return "", ErrConflict
		}
	}
	// The staged result may deliberately omit a secret-bearing parent file.
	// Audit both parent tips too; this does not certify every ancestor.
	for _, candidate := range []string{original, incoming, tree} {
		if err := r.checkTree(ctx, candidate, work, p.MaxTreeBytes, p.Validate, p.Audit); err != nil {
			return "", err
		}
	}
	if head == original {
		r.identity = []string{"GIT_AUTHOR_NAME=" + p.AuthorName, "GIT_AUTHOR_EMAIL=" + p.AuthorEmail,
			"GIT_COMMITTER_NAME=" + p.AuthorName, "GIT_COMMITTER_EMAIL=" + p.AuthorEmail,
			fmt.Sprintf("GIT_AUTHOR_DATE=@%d +0000", p.Time.Unix()), fmt.Sprintf("GIT_COMMITTER_DATE=@%d +0000", p.Time.Unix())}
		candidate, err := r.run(ctx, strings.NewReader(p.Message+"\n"), "commit-tree", tree, "-p", original, "-p", incoming)
		if err != nil {
			return "", err
		}
		candidate = strings.TrimSpace(candidate)
		if !objectID(candidate) {
			return "", ErrInvalid
		}
		if err := unchangedMergeFiles(indexPath, index, mergePath, merge, origPath, orig); err != nil {
			return "", err
		}
		if _, err := r.run(ctx, nil, "update-ref", "-m", p.Message, "HEAD", candidate, original); err != nil {
			return "", err
		}
		head = candidate
	}
	if err := unchangedMergeFiles(indexPath, index, mergePath, merge, origPath, orig); err != nil {
		return "", err
	}
	current, err := r.run(ctx, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(current) != head {
		return "", ErrConflict
	}
	// --quit only forgets merge metadata; unlike --abort it does not restore
	// the old index/worktree. Autostash was explicitly excluded above.
	if _, err := r.run(ctx, nil, "merge", "--quit"); err != nil {
		return "", err
	}
	return head, nil
}

func (r gitRepo) operationPath(ctx context.Context, name string) (string, error) {
	path, err := r.run(ctx, nil, "rev-parse", "--git-path", name)
	if err != nil {
		return "", err
	}
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.dir, path)
	}
	return path, nil
}

func readMergeFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrConflict
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(data)) > limit {
		return nil, ErrConflict
	}
	return data, err
}

func unchangedMergeFiles(indexPath string, index []byte, mergePath string, merge []byte, origPath string, orig []byte) error {
	for _, file := range []struct {
		path string
		data []byte
	}{{indexPath, index}, {mergePath, merge}, {origPath, orig}} {
		got, err := readMergeFile(file.path, int64(len(file.data)))
		if err != nil {
			return err
		}
		if !bytes.Equal(got, file.data) {
			return ErrConflict
		}
	}
	return nil
}
