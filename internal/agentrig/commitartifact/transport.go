package commitartifact

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/process"
)

var ErrTransport = errors.New("retained Git transport failed")

// HTTPCredential is supplied explicitly by the caller, independently of vendor
// session attribution. It is copied at construction, never discovered from the
// worker's login, and never placed in argv, Git config files, or error messages.
type HTTPCredential struct {
	Username string
	Password string `json:"-"`
}

// GitTransportOptions selects one immutable destination and branch. HTTPS and
// absolute local paths are supported. HTTP is restricted to literal loopback
// addresses for local integrations. SSH and ambient credential helpers are not
// supported. CAFile optionally supplies an explicit HTTPS trust bundle; TLS
// verification is always enabled. No option changes canonical Git configuration.
type GitTransportOptions struct {
	Remote, Branch string
	Credential     *HTTPCredential
	CAFile         string
}

// GitTransport implements Transport and Claude's bound-destination interface.
// Methods require a fresh private bare repository owned by the caller, such as
// the repository created by Publish. They must not receive canonical staging or
// a repository with caller-added configuration. Concurrent use of one repoDir
// requires external serialization. No command, queue worker or hook is enabled.
type GitTransport struct{ remote, branch, protocol, caFile, authorization string }

func NewGitTransport(options GitTransportOptions) (*GitTransport, error) {
	if !transportBranch(options.Branch) || len(options.Remote) == 0 || len(options.Remote) > 4096 || strings.ContainsAny(options.Remote, "\x00\r\n") {
		return nil, ErrInvalid
	}
	t := &GitTransport{remote: options.Remote, branch: options.Branch}
	if filepath.IsAbs(options.Remote) {
		t.protocol = "file"
	} else {
		u, err := url.Parse(options.Remote)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(options.Remote, "#") || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || strings.ContainsAny(options.Remote, "\t \\") {
			return nil, ErrInvalid
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()).IsLoopback()) {
			return nil, ErrInvalid
		}
		t.protocol = u.Scheme
	}
	if options.CAFile != "" {
		if t.protocol != "https" || !filepath.IsAbs(options.CAFile) || strings.ContainsAny(options.CAFile, "\x00\r\n") {
			return nil, ErrInvalid
		}
		t.caFile = options.CAFile
	}
	if options.Credential != nil {
		c := *options.Credential
		if t.protocol == "file" || c.Username == "" || c.Password == "" || len(c.Username)+len(c.Password) > 16<<10 || strings.ContainsAny(c.Username, ":\r\n\x00") || strings.ContainsAny(c.Password, "\r\n\x00") {
			return nil, ErrInvalid
		}
		t.authorization = "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(c.Username+":"+c.Password))
	}
	return t, nil
}

func (t *GitTransport) Destination() (string, string) {
	if t == nil {
		return "", ""
	}
	return t.remote, t.branch
}

// Fetch returns empty only when a successful advertisement confirms that the
// exact branch is absent. Authentication, offline, malformed and racing-fetch
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
	if t.protocol == "file" {
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
// through completion. Credentials travel only in a URL-scoped environment
// header. Redirects, helpers, askpass, proxies, inherited Git overrides, submodule
// recursion, hooks and automatic maintenance cannot redirect the operation.
func (t *GitTransport) run(ctx context.Context, dir string, args ...string) (string, int, error) {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	flags := []string{"-c", "credential.helper=", "-c", "credential.interactive=false", "-c", "core.askPass=", "-c", "http.followRedirects=false", "-c", "http.proxy=", "-c", "http.extraHeader=", "-c", "http.sslVerify=true", "-c", "fetch.recurseSubmodules=false", "-c", "submodule.recurse=false", "-c", "fetch.writeCommitGraph=false", "-c", "push.followTags=false", "-c", "push.gpgSign=false"}
	if t.caFile != "" {
		flags = append(flags, "-c", "http.sslCAInfo="+t.caFile, "-c", "http.schannelUseSSLCAInfo=true")
	}
	cmd := (gitRepo{dir: dir}).command(append(flags, args...)...)
	filtered := cmd.Env[:0]
	for _, entry := range cmd.Env {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "GIT_ALLOW_PROTOCOL", "HOME", "USERPROFILE", "XDG_CONFIG_HOME", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "CURL_CA_BUNDLE", "SSL_CERT_FILE", "SSL_CERT_DIR":
			continue
		}
		filtered = append(filtered, entry)
	}
	cmd.Env = append(filtered, "GIT_ALLOW_PROTOCOL="+t.protocol, "GCM_INTERACTIVE=Never", "HOME="+dir, "USERPROFILE="+dir, "XDG_CONFIG_HOME="+dir)
	if t.authorization != "" {
		cmd.Env = append(cmd.Env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http."+t.remote+".extraHeader", "GIT_CONFIG_VALUE_0="+t.authorization)
	}
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

// Avoid including the stored authorization value in ordinary formatted output.
func (*GitTransport) String() string { return "retained Git transport" }

var _ Transport = (*GitTransport)(nil)
