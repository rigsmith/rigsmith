package commitartifact

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

var (
	ErrConflict    = errors.New("retained publication needs conflict resolution")
	ErrUnconfirmed = errors.New("retained publication was not confirmed")
)

// Transport is an explicit, bound destination and branch. Fetch imports that
// branch's complete history into repoDir at ref and returns its commit SHA, or
// empty ONLY for a confirmed absent branch. It must distinguish absence from
// authentication/network failure. Push sends the exact commit with an ordinary
// fast-forward-only push. Neither operation may consult canonical remote config,
// force push, modify other private refs/config, or return before children finish.
// Production authentication and process ownership belong to the transport.
// No production transport or background worker is enabled by this package.
type Transport interface {
	Fetch(ctx context.Context, repoDir, ref string) (string, error)
	Push(ctx context.Context, repoDir, commit string) error
}

// PublishRequest consumes an existing committed artifact. CaptureRef must be the
// exact reference from the sealed batch. The caller validates its binding and
// provenance and holds canonical staging ownership throughout the call.
// LocalDir/LocalCommit optionally identify newer committed synchronous history;
// supply both or neither. Uncommitted files and the canonical index are untouched.
// Validate and Audit inspect the exact raw publication tree, without modifying
// it. They must not require a .git checkout or consult inherited Git settings.
// MaxTreeBytes bounds each materialized tree; zero uses the archive default.
// Attempts bounds push/confirmation cycles (1..10). Conflicts fail closed; no
// mergetool, vendor conflict policy or unrelated-history replacement is implied.
type PublishRequest struct {
	Commits                          artifact.Store
	CommitRef, CaptureRef            string
	LocalDir, LocalCommit            string
	Remote                           Transport
	Message, AuthorName, AuthorEmail string
	Time                             time.Time
	Attempts                         int
	MaxTreeBytes                     int64
	Validate, Audit                  func(context.Context, string) error
}

// Publication is returned only after a fresh remote fetch proves the retained
// capture is reachable, and the observed tree passes validation and auditing.
// A success is evidence for the queue's pushed marker, not an acknowledgement.
type Publication struct{ CaptureCommit, RemoteCommit string }

const remoteRefName = "refs/rig/publication-remote"

// Publish merges retained work with committed local and remote history in a
// private repository. It never rebuilds a missing artifact or updates canonical
// staging. Replaying after a lost push response/queue marker detects the capture
// in remote ancestry. A push command's exit status alone is never confirmation.
func Publish(ctx context.Context, r PublishRequest) (Publication, error) {
	if r.Remote == nil || r.Validate == nil || r.Audit == nil || r.Attempts < 1 || r.Attempts > 10 ||
		!filepath.IsAbs(r.Commits.Dir) || r.CaptureRef == "" || r.MaxTreeBytes < 0 ||
		((r.LocalDir == "") != (r.LocalCommit == "")) ||
		(r.LocalDir != "" && (!filepath.IsAbs(r.LocalDir) || !objectID(r.LocalCommit))) {
		return Publication{}, ErrInvalid
	}
	// Reuse identity validation, including rejection of Git header injection.
	if _, err := requestKey(Request{PolicyID: "publication-v1", Message: r.Message,
		AuthorName: r.AuthorName, AuthorEmail: r.AuthorEmail, Time: r.Time,
		Prepare: r.Validate, Audit: r.Audit}); err != nil {
		return Publication{}, err
	}
	if err := ctx.Err(); err != nil {
		return Publication{}, err
	}
	work, err := os.MkdirTemp(r.Commits.Dir, ".publication-*")
	if err != nil {
		return Publication{}, err
	}
	defer os.RemoveAll(work)
	info, err := Open(ctx, r.Commits, r.CommitRef, filepath.Join(work, "artifact"))
	if err != nil {
		return Publication{}, err
	}
	if info.CaptureRef != r.CaptureRef {
		return Publication{}, ErrInvalid
	}
	repo, err := initRepo(ctx, filepath.Join(work, "git"), info.Commit)
	if err != nil {
		return Publication{}, err
	}
	if err = repo.importRef(ctx, info.BundlePath, RefName, RefName, info.Commit); err != nil {
		return Publication{}, err
	}
	repo.identity = []string{"GIT_AUTHOR_NAME=" + r.AuthorName, "GIT_AUTHOR_EMAIL=" + r.AuthorEmail,
		"GIT_COMMITTER_NAME=" + r.AuthorName, "GIT_COMMITTER_EMAIL=" + r.AuthorEmail,
		fmt.Sprintf("GIT_AUTHOR_DATE=@%d +0000", r.Time.Unix()), fmt.Sprintf("GIT_COMMITTER_DATE=@%d +0000", r.Time.Unix())}

	check := func(sha string) error { return repo.checkTree(ctx, sha, work, r.MaxTreeBytes, r.Validate, r.Audit) }
	observe := func() (string, bool, error) {
		// Never let an absent-branch response reuse a prior successful fetch.
		if _, err := repo.run(ctx, nil, "update-ref", "-d", remoteRefName); err != nil {
			return "", false, err
		}
		sha, err := r.Remote.Fetch(ctx, repo.dir, remoteRefName)
		if err != nil {
			return "", false, err
		}
		if err = ctx.Err(); err != nil {
			return "", false, err
		}
		if sha == "" {
			return "", false, nil
		}
		if !objectID(sha) {
			return "", false, ErrInvalid
		}
		actual, err := repo.run(ctx, nil, "rev-parse", remoteRefName+"^{commit}")
		if err != nil {
			return "", false, err
		}
		if strings.TrimSpace(actual) != sha {
			return "", false, ErrInvalid
		}
		if err = repo.completeHistory(ctx); err != nil {
			return "", false, err
		}
		contains, err := repo.ancestor(ctx, info.Commit, sha)
		if err != nil {
			return "", false, err
		}
		if contains {
			err = check(sha)
		}
		return sha, contains, err
	}
	remote, done, err := observe()
	if err != nil {
		return Publication{}, err
	}
	if done {
		return Publication{info.Commit, remote}, nil
	}
	base := info.Commit
	if r.LocalCommit != "" {
		if err := repo.importRef(ctx, r.LocalDir, r.LocalCommit, "refs/rig/publication-local", r.LocalCommit); err != nil {
			return Publication{}, err
		}
		if err := repo.completeHistory(ctx); err != nil {
			return Publication{}, err
		}
		base, err = repo.merge(ctx, base, r.LocalCommit, r.Message)
		if err != nil {
			return Publication{}, err
		}
	}
	for attempt := 0; attempt < r.Attempts; attempt++ {
		candidate := base
		if remote != "" {
			candidate, err = repo.merge(ctx, base, remote, r.Message)
			if err != nil {
				return Publication{}, err
			}
		}
		if err = check(candidate); err != nil {
			return Publication{}, err
		}
		if err = ctx.Err(); err != nil {
			return Publication{}, err
		}
		pushErr := r.Remote.Push(ctx, repo.dir, candidate)
		if err = ctx.Err(); err != nil {
			return Publication{}, err
		}
		// Even a transport error can mean the server accepted the push. Conversely,
		// a successful exit without a fresh ancestry observation cannot advance work.
		remote, done, err = observe()
		if err != nil {
			return Publication{}, err
		}
		if done {
			return Publication{info.Commit, remote}, nil
		}
		if attempt+1 == r.Attempts {
			return Publication{}, errors.Join(ErrUnconfirmed, pushErr)
		}
	}
	return Publication{}, ErrUnconfirmed
}

func (r gitRepo) ancestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	_, err := r.run(ctx, nil, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if ctx.Err() == nil && errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func (r gitRepo) merge(ctx context.Context, a, b, message string) (string, error) {
	if yes, err := r.ancestor(ctx, b, a); err != nil || yes {
		return a, err
	}
	if yes, err := r.ancestor(ctx, a, b); err != nil || yes {
		return b, err
	}
	// Requires Git's merge-tree --write-tree. No checkout means no filters, symlinks,
	// ignored paths, executable merge drivers or canonical merge state.
	tree, err := r.run(ctx, nil, "merge-tree", "--write-tree", a, b)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", errors.Join(ErrConflict, err)
		}
		return "", err
	}
	tree = strings.TrimSpace(tree)
	if !objectID(tree) {
		return "", ErrInvalid
	}
	sha, err := r.run(ctx, strings.NewReader(message+"\n"), "commit-tree", tree, "-p", a, "-p", b)
	return strings.TrimSpace(sha), err
}

func (r gitRepo) completeHistory(ctx context.Context) error {
	shallow, err := r.run(ctx, nil, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return err
	}
	if strings.TrimSpace(shallow) != "false" {
		return ErrInvalid
	}
	return r.checkObjects(ctx)
}
