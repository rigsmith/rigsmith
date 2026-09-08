package commitartifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/durable"
)

// MergeStagePolicy pins the vendor policy version for one recoverable staging
// operation. Changing policy or event identity requires a separate store.
type MergeStagePolicy struct {
	MergePlanPolicy
	PolicyID string
}

// MergeStageStore retains one immutable repair intent outside every checkout.
// Use a dedicated directory for each merge attempt and retain it until the caller
// has completed the merge. The caller holds the cooperative staging lease; the
// store and canonical directory ancestors must remain stable throughout a call.
// MaxBytes bounds the complete intent archive, not imported Git history.
type MergeStageStore struct {
	Dir        string
	MaxBytes   int64
	afterWrite func(string) error // fault injection, after a durable canonical write
}

type mergeStageFile struct {
	Path                  string
	Before, After         string
	BeforeSize, AfterSize int64
	Mode                  uint32
}
type mergeStageIntent struct {
	Version                                             int
	Binding, Original, Incoming, Commit, Tree, AutoTree string
	Head, Merge, Orig                                   []byte
	IndexBefore, IndexAfter                             string
	Files                                               []mergeStageFile
}

// Stage resolves supported unresolved merges into the canonical index/worktree.
// It requires AUTO_MERGE and unedited affected files, seals the repair before any
// canonical write, and resumes only files/index matching the sealed before or
// after state. Other local edits remain untouched. HEAD and merge metadata stay
// unchanged: FinishStagedMerge is the separate audited completion step. A retry
// after that completion is deliberately refused; callers retain phase state.
func (s MergeStageStore) Stage(ctx context.Context, dir string, p MergeStagePolicy) (tree string, err error) {
	return s.stage(ctx, dir, p, false)
}

// Complete stages and finishes exactly the merge saved in this store. The sealed
// intent remains the restart checkpoint across file writes, HEAD update and Git
// metadata cleanup. Retry requires the saved index and affected files; a different
// HEAD, merge or later affected edit is refused. Callers retain the staging lease
// and the intent until their own durable queue phase records completion.
func (s MergeStageStore) Complete(ctx context.Context, dir string, p MergeStagePolicy) (head string, err error) {
	return s.stage(ctx, dir, p, true)
}

func (s MergeStageStore) stage(ctx context.Context, dir string, p MergeStagePolicy, complete bool) (result string, err error) {
	key, err := requestKey(Request{PolicyID: p.PolicyID, Message: p.Message, AuthorName: p.AuthorName,
		AuthorEmail: p.AuthorEmail, Time: p.Time, Prepare: p.Validate, Audit: p.Audit})
	if err != nil || !filepath.IsAbs(dir) || !filepath.IsAbs(s.Dir) || p.Resolve == nil || p.MaxTreeBytes < 0 || p.MaxBundleBytes < 0 {
		return "", ErrInvalid
	}
	source := gitRepo{dir: dir}
	if _, err := source.planDestination(ctx, s.Dir); err != nil {
		return "", err
	}
	binding, err := source.run(ctx, nil, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	binding, err = filepath.EvalSymlinks(strings.TrimSpace(binding))
	if err != nil {
		return "", err
	}
	key = artifact.Key([]byte("merge-stage-v1\x00" + binding + "\x00" + key))
	store := artifact.Store{Dir: s.Dir, MaxBytes: s.MaxBytes}
	ref, err := store.Build(ctx, key, func(ctx context.Context, root string) error {
		return buildMergeStage(ctx, source, root, binding, p)
	})
	if err != nil {
		return "", err
	}
	work, err := os.MkdirTemp(s.Dir, ".stage-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	sealed := filepath.Join(work, "sealed")
	if err := store.Extract(ctx, ref, sealed); err != nil {
		return "", err
	}
	data, err := readMergeFile(filepath.Join(sealed, "intent.json"), 1<<20)
	if err != nil {
		return "", err
	}
	var intent mergeStageIntent
	if err := json.Unmarshal(data, &intent); err != nil {
		return "", ErrInvalid
	}
	if intent.Version != 1 || intent.Binding != binding || !objectID(intent.Original) || !objectID(intent.Incoming) ||
		!objectID(intent.Commit) || !objectID(intent.Tree) || !objectID(intent.AutoTree) || len(intent.Files) > 1024 {
		return "", ErrInvalid
	}
	repo, err := initRepo(ctx, filepath.Join(work, "git"), intent.Commit)
	if err != nil {
		return "", err
	}
	bundleInfo, err := os.Lstat(filepath.Join(sealed, "candidate.bundle"))
	if err != nil {
		return "", err
	}
	bundleLimit := p.MaxBundleBytes
	if bundleLimit == 0 {
		bundleLimit = artifact.DefaultMaxBytes
	}
	if !bundleInfo.Mode().IsRegular() {
		return "", ErrInvalid
	}
	if bundleInfo.Size() > bundleLimit {
		return "", artifact.ErrTooLarge
	}
	if err := repo.importRef(ctx, filepath.Join(sealed, "candidate.bundle"), "refs/rig/merge-plan", "refs/rig/merge-plan", intent.Commit); err != nil {
		return "", err
	}
	if err := repo.completeHistory(ctx); err != nil {
		return "", err
	}
	parents, err := repo.run(ctx, nil, "show", "-s", "--format=%T%n%P", intent.Commit)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(parents) != intent.Tree+"\n"+intent.Original+" "+intent.Incoming {
		return "", ErrInvalid
	}
	for _, tree := range []string{intent.Original, intent.Incoming, intent.Tree} {
		if err := repo.checkTree(ctx, tree, work, p.MaxTreeBytes, p.Validate, p.Audit); err != nil {
			return "", err
		}
	}
	before, err := readMergeFile(filepath.Join(sealed, "index.before"), 64<<20)
	if err != nil {
		return "", err
	}
	after, err := readMergeFile(filepath.Join(sealed, "index.after"), 64<<20)
	if err != nil {
		return "", err
	}
	if stageDigest(before) != intent.IndexBefore || stageDigest(after) != intent.IndexAfter {
		return "", ErrInvalid
	}
	// The installed index must describe the same tree that was just audited.
	privateIndex := filepath.Join(work, "verify-index")
	if err := os.WriteFile(privateIndex, after, 0600); err != nil {
		return "", err
	}
	indexedRepo := repo
	indexedRepo.identity = []string{"GIT_INDEX_FILE=" + privateIndex}
	indexedTree, err := indexedRepo.run(ctx, nil, "write-tree")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(indexedTree) != intent.Tree {
		return "", ErrInvalid
	}
	for _, file := range intent.Files {
		if !publicationPath(file.Path) || !objectID(file.After) || file.AfterSize < 0 || file.BeforeSize < 0 ||
			(file.Before != "" && !objectID(file.Before)) || (file.Mode != 0600 && file.Mode != 0700) {
			return "", ErrInvalid
		}
		if err := stageFileMatches(ctx, filepath.Join(sealed, "after", filepath.FromSlash(file.Path)), file.After, file.AfterSize, os.FileMode(file.Mode)); err != nil {
			return "", err
		}
	}
	indexPath, err := source.operationPath(ctx, "index")
	if err != nil {
		return "", err
	}
	// A recognizable leftover lock belongs only to this sealed operation. The
	// caller's staging lease establishes that its previous process has stopped.
	release, err := mergeStageIndexLock(indexPath+".lock", ref)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, release()) }()
	if complete {
		head, err := source.run(ctx, nil, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(head) == intent.Commit {
			return s.finish(ctx, source, intent, after, p.MergeFinishPolicy)
		}
	}
	indexed, err := checkMergeStage(ctx, source, intent, before, after)
	if err != nil {
		return "", err
	}
	if err := checkStageFiles(ctx, dir, intent.Files, indexed, false); err != nil {
		return "", err
	}
	// Import objects before the index can name them. No canonical refs are created.
	if _, err := source.run(ctx, nil, "fetch", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", "--",
		filepath.Join(sealed, "candidate.bundle"), "refs/rig/merge-plan"); err != nil {
		return "", err
	}
	for _, file := range intent.Files {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if _, err := checkMergeStage(ctx, source, intent, before, after); err != nil {
			return "", err
		}
		if err := checkStageFiles(ctx, dir, []mergeStageFile{file}, indexed, false); err != nil {
			return "", err
		}
		path := filepath.Join(dir, filepath.FromSlash(file.Path))
		if err := stageParents(dir, file.Path, true); err != nil {
			return "", err
		}
		// Rewriting an already-installed file reflushes an uncertain earlier write.
		if err := stageCopy(ctx, filepath.Join(sealed, "after", filepath.FromSlash(file.Path)), path, os.FileMode(file.Mode)); err != nil {
			return "", err
		}
		if s.afterWrite != nil {
			if err := s.afterWrite(file.Path); err != nil {
				return "", err
			}
		}
	}
	if _, err := checkMergeStage(ctx, source, intent, before, after); err != nil {
		return "", err
	}
	if err := checkStageFiles(ctx, dir, intent.Files, true, false); err != nil {
		return "", err
	}
	if err := durable.Write(ctx, indexPath, func(f *os.File) error { _, err := f.Write(after); return err }); err != nil {
		return "", err
	}
	if s.afterWrite != nil {
		if err := s.afterWrite("index"); err != nil {
			return "", err
		}
	}
	if _, err := checkMergeStage(ctx, source, intent, before, after); err != nil {
		return "", err
	}
	if complete {
		return s.finish(ctx, source, intent, after, p.MergeFinishPolicy)
	}
	return intent.Tree, nil
}

func buildMergeStage(ctx context.Context, source gitRepo, root, binding string, p MergeStagePolicy) error {
	state, err := source.unresolvedPlanState(ctx)
	if err != nil {
		return err
	}
	plan, err := PlanUnresolvedMerge(ctx, source.dir, filepath.Join(root, "plan"), p.MergePlanPolicy)
	if err != nil {
		return err
	}
	auto, err := source.run(ctx, nil, "rev-parse", "--verify", "AUTO_MERGE^{tree}")
	if err != nil {
		return fmt.Errorf("%w: AUTO_MERGE unavailable", ErrConflict)
	}
	auto = strings.TrimSpace(auto)
	if !objectID(auto) {
		return ErrConflict
	}
	work, err := os.MkdirTemp(filepath.Dir(root), ".stage-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	repo, err := initRepo(ctx, filepath.Join(work, "git"), plan.Commit)
	if err != nil {
		return err
	}
	if err := repo.importRef(ctx, plan.BundlePath, "refs/rig/merge-plan", "refs/rig/merge-plan", plan.Commit); err != nil {
		return err
	}
	beforeTree, err := mergeStageTree(ctx, source, auto, p.MaxTreeBytes)
	if err != nil {
		return err
	}
	afterTree, err := mergeStageTree(ctx, repo, plan.Tree, p.MaxTreeBytes)
	if err != nil {
		return err
	}
	// Check AUTO_MERGE against the exact original index. It may differ only at
	// unresolved paths, whose marker bytes are the Git-recorded live baseline.
	indexCopy := filepath.Join(work, "original-index")
	if err := os.WriteFile(indexCopy, state.index, 0600); err != nil {
		return err
	}
	indexed := source
	indexed.identity = []string{"GIT_INDEX_FILE=" + indexCopy}
	flags, err := indexed.run(ctx, nil, "ls-files", "-v", "-z")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(strings.TrimSuffix(flags, "\x00"), "\x00") {
		if len(entry) < 3 || (entry[0] != 'H' && entry[0] != 'M') {
			return ErrConflict
		}
	}
	listing, err := indexed.run(ctx, nil, "ls-files", "--stage", "-z")
	if err != nil {
		return err
	}
	conflicts := map[string]bool{}
	seen := map[string]bool{}
	for _, entry := range strings.Split(strings.TrimSuffix(listing, "\x00"), "\x00") {
		header, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) != 3 {
			return ErrConflict
		}
		file := beforeTree[path]
		if file == nil {
			return ErrConflict
		}
		mode := "100644"
		if file.mode == 0700 {
			mode = "100755"
		}
		if fields[0] != mode {
			return ErrConflict
		}
		if fields[2] == "0" {
			if fields[1] != file.oid {
				return ErrConflict
			}
		} else {
			conflicts[path] = true
		}
		seen[path] = true
	}
	if len(seen) != len(beforeTree) {
		return ErrConflict
	}
	intent := mergeStageIntent{Version: 1, Binding: binding, Original: plan.Original, Incoming: plan.Incoming,
		Commit: plan.Commit, Tree: plan.Tree, AutoTree: auto, Head: state.head, Merge: state.merge, Orig: state.orig, IndexBefore: stageDigest(state.index)}
	// Sorted Git tree order makes the write/replay sequence stable.
	raw, err := repo.run(ctx, nil, "ls-tree", "-r", "--name-only", "-z", plan.Tree)
	if err != nil {
		return err
	}
	for _, path := range strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00") {
		after := afterTree[path]
		if after == nil {
			return ErrInvalid
		}
		before := beforeTree[path]
		// A conflict is still affected when its resolution keeps AUTO_MERGE bytes.
		// Retain it so initial validation and replay cannot skip later live edits.
		if before != nil && !conflicts[path] && before.oid == after.oid && before.mode == after.mode {
			continue
		}
		file := mergeStageFile{Path: path, After: after.oid, AfterSize: after.size, Mode: uint32(after.mode)}
		if before != nil {
			if !conflicts[path] || before.mode != after.mode {
				return ErrConflict
			}
			file.Before = before.oid
			file.BeforeSize = before.size
		}
		intent.Files = append(intent.Files, file)
	}
	for path := range beforeTree {
		if afterTree[path] == nil {
			return ErrConflict
		}
	}
	if len(intent.Files) > 1024 {
		return artifact.ErrTooLarge
	}
	if err := checkStageFiles(ctx, source.dir, intent.Files, false, true); err != nil {
		return err
	}
	for _, side := range []string{"before", "after"} {
		dest := filepath.Join(root, side)
		if err := os.Mkdir(dest, 0700); err != nil {
			return err
		}
		var files []*publicationFile
		for _, file := range intent.Files {
			f := afterTree[file.Path]
			if side == "before" {
				f = beforeTree[file.Path]
			}
			if f == nil {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dest, filepath.FromSlash(f.path))), 0700); err != nil {
				return err
			}
			files = append(files, f)
		}
		r := repo
		if side == "before" {
			r = source
		}
		if err := r.materializeBlobs(ctx, dest, files); err != nil {
			return err
		}
	}
	if _, err := repo.run(ctx, nil, "read-tree", plan.Tree); err != nil {
		return err
	}
	afterIndex, err := readMergeFile(filepath.Join(repo.dir, "index"), 64<<20)
	if err != nil {
		return err
	}
	intent.IndexAfter = stageDigest(afterIndex)
	current, err := source.unresolvedPlanState(ctx)
	if err != nil {
		return err
	}
	if !state.equal(current) {
		return ErrConflict
	}
	if err := os.WriteFile(filepath.Join(root, "index.before"), state.index, 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "index.after"), afterIndex, 0600); err != nil {
		return err
	}
	data, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return artifact.ErrTooLarge
	}
	if err := os.WriteFile(filepath.Join(root, "intent.json"), data, 0600); err != nil {
		return err
	}
	if err := os.Rename(plan.BundlePath, filepath.Join(root, "candidate.bundle")); err != nil {
		return err
	}
	return os.Remove(filepath.Dir(plan.BundlePath))
}

func mergeStageTree(ctx context.Context, r gitRepo, tree string, limit int64) (map[string]*publicationFile, error) {
	if limit == 0 {
		limit = artifact.DefaultMaxBytes
	}
	var out bytes.Buffer
	if err := r.runTo(ctx, nil, &boundedOutput{w: &out, left: publicationMetadataLimit}, "ls-tree", "-rltz", "--full-tree", tree); err != nil {
		return nil, err
	}
	parsed, err := parsePublicationTree(out.Bytes(), limit, publicationMetadataLimit)
	if err != nil {
		return nil, err
	}
	files := make(map[string]*publicationFile, len(parsed.files))
	for _, file := range parsed.files {
		files[file.path] = file
	}
	return files, nil
}
func stageDigest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func checkMergeStage(ctx context.Context, r gitRepo, i mergeStageIntent, before, after []byte) (bool, error) {
	state, err := r.unresolvedPlanState(ctx)
	if err != nil {
		return false, err
	}
	if state.original != i.Original || state.incoming != i.Incoming || !bytes.Equal(state.head, i.Head) || !bytes.Equal(state.merge, i.Merge) || !bytes.Equal(state.orig, i.Orig) {
		return false, ErrConflict
	}
	auto, err := r.run(ctx, nil, "rev-parse", "--verify", "AUTO_MERGE^{tree}")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(auto) != i.AutoTree {
		return false, ErrConflict
	}
	if bytes.Equal(state.index, after) {
		return true, nil
	}
	if !bytes.Equal(state.index, before) {
		return false, ErrConflict
	}
	return false, nil
}

func checkStageFiles(ctx context.Context, dir string, files []mergeStageFile, installed, initial bool) error {
	for _, file := range files {
		if err := stageParents(dir, file.Path, false); err != nil {
			return err
		}
		path := filepath.Join(dir, filepath.FromSlash(file.Path))
		if !initial && stageFileMatches(ctx, path, file.After, file.AfterSize, os.FileMode(file.Mode)) == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if installed {
			return ErrConflict
		}
		if file.Before == "" {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				return ErrConflict
			}
		} else if err := stageFileMatches(ctx, path, file.Before, file.BeforeSize, os.FileMode(file.Mode)); err != nil {
			if cancelled := ctx.Err(); cancelled != nil {
				return cancelled
			}
			return fmt.Errorf("%w: affected merge file changed", ErrConflict)
		}
	}
	return ctx.Err()
}
func stageFileMatches(ctx context.Context, path, oid string, size int64, mode os.FileMode) error {
	node := publicationNode{file: &publicationFile{oid: oid, size: size, mode: mode}}
	if err := node.verify(ctx, path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" && (info.Mode().Perm()&0111 != 0) != (mode&0111 != 0) {
		return ErrConflict
	}
	return nil
}

// Reject symlink ancestors; missing directories are created only after all file
// and index preflight checks pass. New directory entries are flushed by Write.
func stageParents(dir, path string, create bool) error {
	parts := strings.Split(path, "/")
	current := dir
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if !create {
				continue
			}
			if err := os.Mkdir(current, 0700); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if !info.IsDir() {
			return ErrConflict
		}
	}
	return nil
}
func stageCopy(ctx context.Context, from, to string, mode os.FileMode) error {
	return durable.Write(ctx, to, func(out *os.File) error {
		in, err := os.Open(from)
		if err != nil {
			return err
		}
		defer in.Close()
		if _, err := io.Copy(out, in); err != nil {
			return err
		}
		return out.Chmod(mode)
	})
}
func mergeStageIndexLock(path, ref string) (func() error, error) {
	token := []byte("rig-merge-stage-v1\n" + ref + "\n")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		old, readErr := readMergeFile(path, 256)
		if readErr != nil || !bytes.Equal(old, token) {
			return nil, ErrConflict
		}
	} else if err != nil {
		return nil, err
	} else {
		_, writeErr := f.Write(token)
		closeErr := f.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			_ = os.Remove(path)
			return nil, err
		}
	}
	return func() error {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}, nil
}
