package commitartifact

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/process"
)

var ErrTransport = errors.New("retained Git transport failed")

// GitTransportOptions selects one absolute local repository path and branch.
// Network URLs and remote aliases are refused. This adapter supports retained
// publication fixtures; network integration will reuse the existing Git/gh path.
type GitTransportOptions struct {
	Remote, Branch string
}

// GitTransport implements Transport and Claude's bound-destination interface for
// local repositories only. Methods require a fresh private bare repository owned
// by the caller, such as the repository created by Publish, with no caller-added
// configuration. Concurrent use of one repoDir requires external serialization.
// No command, queue worker or hook is enabled.
type GitTransport struct{ remote, branch string }

func NewGitTransport(options GitTransportOptions) (*GitTransport, error) {
	if !transportBranch(options.Branch) || !filepath.IsAbs(options.Remote) || len(options.Remote) > 4096 || strings.ContainsAny(options.Remote, "\x00\r\n") {
		return nil, ErrInvalid
	}
	return &GitTransport{remote: options.Remote, branch: options.Branch}, nil
}

func (t *GitTransport) Destination() (string, string) {
	if t == nil {
		return "", ""
	}
	return t.remote, t.branch
}

// Fetch returns empty only when a successful advertisement confirms that the
// exact branch is absent. Missing repositories, malformed and racing-fetch
// failures remain errors. Only the supplied publication ref is modified.
func (t *GitTransport) Fetch(ctx context.Context, repoDir, ref string) (string, error) {
	if !strings.HasPrefix(ref, "refs/rig/publication-") || !transportBranch(strings.TrimPrefix(ref, "refs/")) {
		return "", ErrInvalid
	}
	if err := t.checkRepo(ctx, repoDir); err != nil {
		return "", err
	}
	if _, _, err := t.run(ctx, repoDir, "update-ref", "-d", ref); err != nil {
		return "", err
	}
	branch := "refs/heads/" + t.branch
	listing, code, err := t.run(ctx, repoDir, "ls-remote", "--exit-code", "--refs", "--", t.remote, branch)
	if code == 2 && ctx.Err() == nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	fields := strings.Split(strings.TrimSuffix(listing, "\n"), "\t")
	if len(fields) != 2 || !objectID(fields[0]) || fields[1] != branch {
		return "", ErrTransport
	}
	// The branch can advance after advertisement. Return the SHA actually fetched,
	// which Publish independently verifies and checks for complete ancestry.
	if _, _, err = t.run(ctx, repoDir, "fetch", "--no-tags", "--no-write-fetch-head", "--no-auto-maintenance", "--recurse-submodules=no", "--", t.remote, branch+":"+ref); err != nil {
		return "", err
	}
	actual, _, err := t.run(ctx, repoDir, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	actual = strings.TrimSpace(actual)
	if !objectID(actual) {
		return "", ErrTransport
	}
	return actual, nil
}

// Push sends exactly commit to the bound branch using ordinary fast-forward
// semantics. Its success is not confirmation; Publish always fetches afterward.
func (t *GitTransport) Push(ctx context.Context, repoDir, commit string) error {
	if !objectID(commit) {
		return ErrInvalid
	}
	if err := t.checkRepo(ctx, repoDir); err != nil {
		return err
	}
	actual, _, err := t.run(ctx, repoDir, "rev-parse", "--verify", commit+"^{commit}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(actual) != commit {
		return ErrInvalid
	}
	_, _, err = t.run(ctx, repoDir, "push", "--porcelain", "--no-verify", "--recurse-submodules=no", "--", t.remote, commit+":refs/heads/"+t.branch)
	return err
}

func (t *GitTransport) checkRepo(ctx context.Context, dir string) error {
	if t == nil || t.remote == "" || !filepath.IsAbs(dir) {
		return ErrInvalid
	}
	local, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return ErrInvalid
	}
	remote, err := filepath.EvalSymlinks(t.remote)
	if err != nil {
		return ErrTransport
	}
	local, remote = strings.ToLower(filepath.Clean(local)), strings.ToLower(filepath.Clean(remote))
	if local == remote || strings.HasPrefix(local, remote+string(filepath.Separator)) || strings.HasPrefix(remote, local+string(filepath.Separator)) {
		return ErrInvalid
	}
	bare, _, err := t.run(ctx, dir, "rev-parse", "--is-bare-repository")
	if err != nil {
		return err
	}
	if strings.TrimSpace(bare) != "true" {
		return ErrInvalid
	}
	return nil
}

func transportBranch(branch string) bool {
	if len(branch) == 0 || len(branch) > 256 || strings.HasPrefix(branch, "-") || strings.Contains(branch, "..") || strings.Contains(branch, "@{") {
		return false
	}
	for _, ch := range branch {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("/-_.", ch)) {
			return false
		}
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

// run bounds diagnostics, suppresses raw Git errors/argv, and owns every helper
// through completion. The private Git runner permits only local file transport
// and disables inherited Git overrides, hooks and automatic maintenance.
func (t *GitTransport) run(ctx context.Context, dir string, args ...string) (string, int, error) {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	flags := []string{"-c", "fetch.recurseSubmodules=false", "-c", "submodule.recurse=false", "-c", "fetch.writeCommitGraph=false", "-c", "push.followTags=false", "-c", "push.gpgSign=false"}
	cmd := (gitRepo{dir: dir}).command(append(flags, args...)...)
	var out bytes.Buffer
	bound := &boundedOutput{w: &out, left: gitOutputLimit}
	cmd.Stdout = &cancelOutput{writer: bound, cancel: cancel}
	err := process.Run(childCtx, cmd)
	code := -1
	if _, pureExit := err.(*exec.ExitError); cmd.ProcessState != nil && (err == nil || pureExit) {
		code = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		return "", code, ctx.Err()
	}
	if bound.exceeded {
		return "", -1, artifact.ErrTooLarge
	}
	if err != nil {
		return "", code, fmt.Errorf("%w (exit %d)", ErrTransport, code)
	}
	return out.String(), code, nil
}

var _ Transport = (*GitTransport)(nil)
