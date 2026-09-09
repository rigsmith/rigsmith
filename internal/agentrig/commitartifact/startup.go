package commitartifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var ErrSharedHistory = errors.New("initialized shared Git history required")

// StartupHistoryRequest checks an exact committed canonical seed against a fresh
// remote observation. The caller holds staging ownership and supplies a private
// ScratchParent outside source/backup roots. LocalCommit must come from
// SettledHead under that same lease. Empty local or remote history requires
// explicit initialization; this check never creates a root commit or pushes.
type StartupHistoryRequest struct {
	LocalDir, LocalCommit, ScratchParent string
	Remote                               Transport
}

// StartupHistory records the two observed tips and one common ancestor. It is
// diagnostic evidence for this startup only, not a publication receipt, content
// audit or promise that later remote changes will remain compatible.
type StartupHistory struct{ LocalCommit, RemoteCommit, CommonAncestor string }

// CheckStartupHistory imports committed objects into a disposable bare repository
// and requires complete, related histories. Either tip may be ahead, or both may
// have diverged from a common ancestor. No canonical refs, index or working files
// change. Existing publication validation still runs for every batch.
func CheckStartupHistory(ctx context.Context, req StartupHistoryRequest) (StartupHistory, error) {
	fail := StartupHistory{}
	if err := ctx.Err(); err != nil {
		return fail, err
	}
	if req.Remote == nil || !filepath.IsAbs(req.LocalDir) || !filepath.IsAbs(req.ScratchParent) {
		return fail, ErrInvalid
	}
	if req.LocalCommit == "" {
		return fail, ErrSharedHistory
	}
	if !objectID(req.LocalCommit) {
		return fail, ErrInvalid
	}
	if err := (gitRepo{dir: req.LocalDir}).completeHistory(ctx); err != nil {
		return fail, err
	}
	work, err := os.MkdirTemp(req.ScratchParent, ".startup-history-*")
	if err != nil {
		return fail, err
	}
	defer os.RemoveAll(work)
	repo, err := initRepo(ctx, filepath.Join(work, "git"), req.LocalCommit)
	if err != nil {
		return fail, err
	}
	if err := repo.importRef(ctx, req.LocalDir, req.LocalCommit, "refs/rig/publication-local", req.LocalCommit); err != nil {
		return fail, err
	}
	tip, err := req.Remote.Fetch(ctx, repo.dir, remoteRefName)
	if err != nil {
		return fail, err
	}
	if err := ctx.Err(); err != nil {
		return fail, err
	}
	if tip == "" {
		return fail, ErrSharedHistory
	}
	if !objectID(tip) {
		return fail, ErrInvalid
	}
	actual, err := repo.run(ctx, nil, "rev-parse", remoteRefName+"^{commit}")
	if err != nil {
		return fail, err
	}
	if strings.TrimSpace(actual) != tip {
		return fail, ErrInvalid
	}
	if err := repo.completeHistory(ctx); err != nil {
		return fail, err
	}
	base, err := repo.run(ctx, nil, "merge-base", req.LocalCommit, tip)
	if err != nil {
		if gitExited(err, 1) {
			return fail, ErrSharedHistory
		}
		return fail, err
	}
	base = strings.TrimSpace(base)
	if !objectID(base) {
		return fail, ErrInvalid
	}
	return StartupHistory{req.LocalCommit, tip, base}, nil
}
