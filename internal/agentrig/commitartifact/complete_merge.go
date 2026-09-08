package commitartifact

import (
	"bytes"
	"context"
	"os"
	"strings"
)

// finish consumes the already verified sealed candidate under the staging and
// owned index locks. Matching ancestry alone never establishes completion.
func (s MergeStageStore) finish(ctx context.Context, r gitRepo, intent mergeStageIntent, index []byte, p MergeFinishPolicy) (string, error) {
	if err := checkMergeCompletion(ctx, r, intent, index); err != nil {
		return "", err
	}
	if err := r.completeHistory(ctx); err != nil {
		return "", err
	}
	head, err := r.run(ctx, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(head) == intent.Original {
		// Publish the verified retained candidate itself. Recreating it in the
		// canonical repo could inherit a different commit-encoding setting.
		if _, err := r.run(ctx, nil, "update-ref", "-m", p.Message, "HEAD", intent.Commit, intent.Original); err != nil {
			return "", err
		}
		if s.afterWrite != nil {
			if err := s.afterWrite("head"); err != nil {
				return "", err
			}
		}
	}
	head, err = FinishStagedMerge(ctx, r.dir, p)
	if err != nil {
		return "", err
	}
	if head != intent.Commit {
		return "", ErrConflict
	}
	// A process can stop after Git removes MERGE_HEAD but before the remaining
	// merge metadata. FinishStagedMerge then sees settled HEAD; finish cleanup
	// only after it has also excluded every other active operation.
	if _, err := r.run(ctx, nil, "merge", "--quit"); err != nil {
		return "", err
	}
	if s.afterWrite != nil {
		if err := s.afterWrite("complete"); err != nil {
			return "", err
		}
	}
	if err := checkMergeCompletion(ctx, r, intent, index); err != nil {
		return "", err
	}
	settled, err := SettledHead(ctx, r.dir)
	if err != nil {
		return "", err
	}
	if settled != intent.Commit {
		return "", ErrConflict
	}
	return settled, nil
}

func checkMergeCompletion(ctx context.Context, r gitRepo, i mergeStageIntent, index []byte) error {
	head, err := r.run(ctx, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	head = strings.TrimSpace(head)
	if head != i.Original && head != i.Commit {
		return ErrConflict
	}
	// A detached HEAD changes its file bytes when committed; symbolic HEAD must
	// keep naming the same branch, even if another branch has the same commit.
	expectedHead := i.Head
	if head == i.Commit && !bytes.HasPrefix(expectedHead, []byte("ref: ")) {
		expectedHead = []byte(i.Commit + "\n")
	}
	for _, file := range []struct {
		name string
		data []byte
	}{{"HEAD", expectedHead}, {"ORIG_HEAD", i.Orig}, {"index", index}} {
		path, err := r.operationPath(ctx, file.name)
		if err != nil {
			return err
		}
		got, err := readMergeFile(path, int64(len(file.data)))
		if err != nil {
			return err
		}
		if !bytes.Equal(got, file.data) {
			return ErrConflict
		}
	}
	for _, name := range []string{"MERGE_HEAD", "AUTO_MERGE"} {
		path, err := r.operationPath(ctx, name)
		if err != nil {
			return err
		}
		got, err := readMergeFile(path, 256)
		if os.IsNotExist(err) && head == i.Commit {
			continue // Git may already have removed all or part of its metadata.
		}
		if err != nil {
			return err
		}
		want := i.Merge
		if name == "AUTO_MERGE" {
			want = []byte(i.AutoTree + "\n")
		}
		if !bytes.Equal(got, want) {
			return ErrConflict
		}
	}
	return checkStageFiles(ctx, r.dir, i.Files, true, false)
}
