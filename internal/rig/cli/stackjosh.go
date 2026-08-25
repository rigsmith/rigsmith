package cli

// josh engine management for `rig stack` — the josh-sync pattern (rust-lang):
// rig owns a pinned josh-proxy binary, spawns it as an ephemeral localhost
// process per operation, and does all history work with plain git against
// URLs that carry the filter in the path. No daemon, no user-managed install.

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// stackJoshVersion is the josh tag rig installs and expects. Pinned because the
// filter algebra must be deterministic against existing workspace history;
// a workspace may override via the manifest's `josh` key, at its own risk.
const stackJoshVersion = "r26.07.19"

const stackJoshRepo = "https://github.com/josh-project/josh"

// stackJoshDir is where rig keeps its own josh installs, one per version, so an
// engine upgrade is a new directory rather than an in-place mutation.
func stackJoshDir(version string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "rigsmith", "josh", version), nil
}

func stackJoshProxyBin(version string) (string, error) {
	dir, err := stackJoshDir(version)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "bin", "josh-proxy"), nil
}

// ensureJoshProxy returns the pinned josh-proxy binary, installing it via the
// user's cargo on first use. The install is a full Rust build — minutes, said
// out loud on out — which is why it happens here (and in `stack doctor --fix`)
// rather than silently inside a verb that looked instant.
func ensureJoshProxy(ctx context.Context, version string, out io.Writer) (string, error) {
	bin, err := stackJoshProxyBin(version)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(bin); err == nil {
		return bin, nil
	}
	cargo, err := exec.LookPath("cargo")
	if err != nil {
		return "", fmt.Errorf("josh-proxy %s is not installed and cargo was not found; install rust (https://rustup.rs) and re-run", version)
	}
	dir, err := stackJoshDir(version)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(out, "installing josh-proxy %s (a Rust build — takes a few minutes, one time per version)…\n", version)
	cmd := exec.CommandContext(ctx, cargo, "install", "--locked",
		"--git", stackJoshRepo, "--tag", version, "--root", dir, "josh-proxy")
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("cargo install josh-proxy: %w", err)
	}
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("cargo install finished but %s is missing", bin)
	}
	return bin, nil
}

// joshProxy is one running ephemeral proxy, bound to a single remote host.
type joshProxy struct {
	cmd    *exec.Cmd
	port   int
	host   string
	exited chan struct{} // closed once the process has been reaped (exactly one Wait)
}

// startJoshProxy spawns josh-proxy fronting https://<host> on a free port and
// waits for it to accept connections. The port is picked fresh per invocation —
// unlike josh-sync's fixed 42042 — so two rig commands can't collide.
func startJoshProxy(ctx context.Context, bin, host string) (*joshProxy, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	local := filepath.Join(cache, "rigsmith", "josh", host)
	if err := os.MkdirAll(local, 0o755); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin,
		"--local", local,
		"--remote", "https://"+host,
		fmt.Sprintf("--port=%d", port),
		"--no-background")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting josh-proxy: %w", err)
	}
	p := &joshProxy{cmd: cmd, port: port, host: host, exited: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(p.exited) }() // sole reaper; stop() only observes
	// Poll until the port answers. Budget is generous — a cold proxy opening a
	// large --local cache is slower than josh-sync's 1s assumption.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return p, nil
		}
		select {
		case <-p.exited:
			return nil, fmt.Errorf("josh-proxy exited before becoming ready (port %d)", port)
		case <-time.After(10 * time.Millisecond):
		}
	}
	p.stop()
	return nil, fmt.Errorf("josh-proxy did not become ready on port %d", port)
}

// stop shuts the proxy down, gracefully first so its --local cache is left
// consistent for the next invocation.
func (p *joshProxy) stop() {
	select {
	case <-p.exited:
		return // already gone
	default:
	}
	_ = p.cmd.Process.Signal(os.Interrupt)
	select {
	case <-p.exited:
	case <-time.After(2 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.exited
	}
}

// url builds the filtered git URL: /owner/name.git[@commit]<filter>.git with
// the filter percent-encoded, mirroring josh-sync's construction. commit pins
// the fetch to an exact upstream SHA so a pull imports what the cursor check
// saw, not whatever the branch moved to in between.
func (p *joshProxy) url(repoPath, commit, filter string) string {
	at := ""
	if commit != "" {
		at = "@" + commit
	}
	return fmt.Sprintf("http://127.0.0.1:%d/%s.git%s%s.git", p.port, repoPath, at, url.QueryEscape(filter))
}

// stackPrefixFilter is the josh filter that maps a whole upstream repo under a
// workspace prefix (and, reversed on push, back out of it).
func stackPrefixFilter(prefix string) string { return ":prefix=" + prefix }

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
