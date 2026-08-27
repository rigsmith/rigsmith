package cli

// josh engine management for `rig stack` — the josh-sync pattern (rust-lang):
// rig owns a pinned josh-proxy binary, spawns it as an ephemeral localhost
// process per operation, and does all history work with plain git against
// URLs that carry the filter in the path. No daemon, no user-managed install.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// stackJoshVersion is the josh tag rig installs and expects. Pinned because the
// filter algebra must be deterministic against existing workspace history;
// a workspace may override via the manifest's `josh` key, at its own risk.
const stackJoshVersion = "r26.07.19"

const stackJoshRepo = "https://github.com/josh-project/josh"

// stackJoshRelease is the rigsmith/josh-binaries release the engine is fetched
// from. It tracks a josh tag, with a -win.N suffix while the Windows port is
// carried as a patch (josh-project/josh#2512); the suffix goes away once that
// lands and tags build unmodified everywhere.
const stackJoshRelease = "r26.07.19-win.3"

const stackJoshBinaries = "https://github.com/rigsmith/josh-binaries/releases/download"

// stackJoshMaxBytes bounds the engine download. The real binaries are ~30 MiB;
// this is a ceiling on damage, not a size expectation.
const stackJoshMaxBytes int64 = 256 << 20

// stackJoshTools are the engine binaries rig installs. josh-proxy serves the
// filtered history over http, which is how a repo is imported; josh-filter
// rewrites refs in a local repository, which is how a member is extracted again
// with its history. Same release, same verification, different jobs.
const (
	toolProxy  = "josh-proxy"
	toolFilter = "josh-filter"
)

// stackJoshChecksums pins what each binary must hash to on each platform.
// Pinned here rather than read from the release, so that trusting a download
// does not reduce to trusting whoever can write to the release.
var stackJoshChecksums = map[string]map[string]string{
	toolProxy: {
		"linux-arm64":   "685728fae9346cbbcda43ad372b02d447f873e8121f12de1403fac02b8533763",
		"linux-x64":     "845c717891965242ce716f88efdec76eb0ed96e45ecc63933ef2f6d544d2638d",
		"macos-arm64":   "f353cf4c845152347bd2bf645065267d0b48096af5b802f8abefcfdba565ac74",
		"macos-x64":     "c659519ba3aa855053e03adbe723bee5de88c6c9850a95775b4de83d6065a172",
		"windows-arm64": "c96ef9eb891c2b270f65d6fea3d62cdf43628674dac4801ad359e3af699121b9",
		"windows-x64":   "2ae71b33b80cfc4fac70f0e1e6ac4878671237630e628fc9b86eebb6d8f149ee",
	},
	toolFilter: {
		"linux-arm64":   "01792ee8a4a5770d5af6d3f505dbec12769a3006cd0417f84dd15184e72e098f",
		"linux-x64":     "a1cb4e50a1c7fc5b3e252e941e793843fd247f1e89af64a7a32fa2a06e788980",
		"macos-arm64":   "13e8fd3ce187987c07957fec462c0e65c144fe592ac94bc0640a2188a131ee22",
		"macos-x64":     "02e129dee85e760dd4caaea82255e351e6675411a6b19a5c59c2ea9080b2bdfd",
		"windows-arm64": "05a69276f9d890465755e2cdf6366d091ccd809bc2c569385784a5b0c946ce3c",
		"windows-x64":   "c91222e64c5cda7ec9ef3f94df4f9177a9b05b7e81d8a17c03afd6d40fba5625",
	},
}

// stackJoshTarget names this platform the way the release assets do.
func stackJoshTarget() (string, error) {
	var os_, arch string
	switch runtime.GOOS {
	case "linux":
		os_ = "linux"
	case "darwin":
		os_ = "macos"
	case "windows":
		os_ = "windows"
	default:
		return "", fmt.Errorf("no josh binaries are published for %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	default:
		return "", fmt.Errorf("no josh binaries are published for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return os_ + "-" + arch, nil
}

// downloadJoshTool fetches one pinned engine binary for this platform into
// dest. It verifies the checksum when one is pinned, and refuses a download it
// cannot check rather than trusting the bytes.
func downloadJoshTool(ctx context.Context, tool, dest string, out io.Writer) error {
	target, err := stackJoshTarget()
	if err != nil {
		return err
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	asset := fmt.Sprintf("%s-%s-%s%s", tool, stackJoshRelease, target, ext)
	url := fmt.Sprintf("%s/%s/%s", stackJoshBinaries, stackJoshRelease, asset)

	want, pinned := stackJoshChecksums[tool][target]
	if !pinned {
		return fmt.Errorf("no checksum is pinned for %s, so %s cannot be verified", target, asset)
	}

	fmt.Fprintf(out, "fetching %s %s for %s\n", tool, stackJoshRelease, target)
	// The command's context carries no deadline of its own, and a release server
	// that accepts the connection then stops sending would hang the verb forever.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: %s", asset, resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	// Write beside the target and rename, so an interrupted download never
	// leaves something executable in place.
	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+tool+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	sum := sha256.New()
	// Cap the read: the checksum only decides whether the bytes are *run*, so
	// without a limit a malformed release could fill the disk before we reject it.
	n, err := io.Copy(io.MultiWriter(tmp, sum), io.LimitReader(resp.Body, stackJoshMaxBytes+1))
	if err != nil {
		tmp.Close()
		return err
	}
	if n > stackJoshMaxBytes {
		tmp.Close()
		return fmt.Errorf("%s is larger than %d MiB and was not written", asset, stackJoshMaxBytes>>20)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != want {
		return fmt.Errorf("%s does not match its pinned checksum (got %s, want %s)", asset, got, want)
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}

// stackJoshDir is where rig keeps its own josh installs, one per version, so an
// engine upgrade is a new directory rather than an in-place mutation.
func stackJoshDir(version string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "rigsmith", "josh", version), nil
}

func stackJoshToolBin(version, tool string) (string, error) {
	dir, err := stackJoshDir(version)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "bin", tool+stackExeSuffix()), nil
}

func stackJoshProxyBin(version string) (string, error) {
	return stackJoshToolBin(version, toolProxy)
}

// stackExeSuffix is what this platform's executables are named with — the
// downloaded asset and cargo's output both carry it, so the installed path has
// to as well or neither can be found again.
func stackExeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// stackJoshInstalled reports whether bin is something we can actually run. A
// bare os.Stat is satisfied by a directory of that name, which would let doctor
// call the engine healthy and --fix skip a repair it needed to do.
func stackJoshInstalled(bin string) error {
	fi, err := os.Stat(bin)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a file", bin)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s is not executable", bin)
	}
	return nil
}

// ensureJoshProxy returns the pinned josh-proxy binary, installing it via the
// user's cargo on first use. The install is a full Rust build — minutes, said
// out loud on out — which is why it happens here (and in `stack doctor --fix`)
// rather than silently inside a verb that looked instant.
func ensureJoshTool(ctx context.Context, version, tool string, out io.Writer) (string, error) {
	bin, err := stackJoshToolBin(version, tool)
	if err != nil {
		return "", err
	}
	if err := stackJoshInstalled(bin); err == nil {
		return bin, nil
	}
	// A published binary is seconds; building josh is minutes. Only fall back
	// to cargo when this platform has no binary, or the download fails.
	if version == stackJoshVersion {
		if err := downloadJoshTool(ctx, tool, bin, out); err == nil {
			return bin, nil
		} else {
			fmt.Fprintf(out, "could not fetch a published %s (%v); building from source\n", tool, err)
		}
	}

	// Windows support lives in the patched release (the -win suffix), not in the
	// upstream tag: building that tag from source would produce a binary that
	// cannot run here, so say why instead of spending minutes on it.
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("could not fetch the published %s for windows, and building %s from source would omit the Windows support that %s carries; retry, or install a binary from %s manually",
			tool, version, stackJoshRelease, stackJoshBinaries)
	}
	cargo, err := exec.LookPath("cargo")
	if err != nil {
		return "", fmt.Errorf("%s %s is not installed, no published binary could be fetched, and cargo was not found; install rust (https://rustup.rs) and re-run", tool, version)
	}
	dir, err := stackJoshDir(version)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(out, "installing %s %s (a Rust build — takes a few minutes, one time per version)…\n", tool, version)
	cmd := exec.CommandContext(ctx, cargo, "install", "--locked",
		"--git", stackJoshRepo, "--tag", version, "--root", dir, tool)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("cargo install %s: %w", tool, err)
	}
	if err := stackJoshInstalled(bin); err != nil {
		return "", fmt.Errorf("cargo install finished but %s is unusable: %w", bin, err)
	}
	return bin, nil
}

// ensureJoshProxy is ensureJoshTool for the proxy, which most of the stack verbs
// want and none of them should have to name.
func ensureJoshProxy(ctx context.Context, version string, out io.Writer) (string, error) {
	return ensureJoshTool(ctx, version, toolProxy, out)
}

// joshProxy is one running ephemeral proxy, bound to a single remote host.
type joshProxy struct {
	cmd    *exec.Cmd
	port   int
	host   string
	log    string        // where the engine's own output went, for diagnosing failures
	exited chan struct{} // closed once the process has been reaped (exactly one Wait)
}

// startJoshProxy spawns josh-proxy fronting https://<host> on a free port and
// waits for it to accept connections. The port is picked fresh per invocation —
// unlike josh-sync's fixed 42042 — so two rig commands can't collide.
func startJoshProxy(ctx context.Context, bin, host string) (*joshProxy, error) {
	port, err := freePort(ctx)
	if err != nil {
		return nil, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	// git writes this path into .git/objects/info/alternates, where a colon
	// separates entries: a host carrying a port would be split in half and the
	// fetch would fail claiming the repository is corrupt.
	local := filepath.Join(cache, "rigsmith", "josh", strings.ReplaceAll(host, ":", "_"))
	// 0700: this cache holds the objects of whatever was pulled through it,
	// which for a private upstream is content no other local user should read.
	if err := os.MkdirAll(local, 0o700); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin,
		"--local", local,
		"--remote", stackRemoteScheme(host)+stackHostForURL(host),
		"--port="+strconv.Itoa(port),
		"--no-background")
	// Keep the engine's output: when a filter or fetch fails, its log is the
	// only place that says why, and discarding it leaves the caller guessing.
	logFile, err := os.CreateTemp("", "josh-proxy-*.log")
	if err != nil {
		return nil, err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("starting josh-proxy: %w", err)
	}
	p := &joshProxy{cmd: cmd, port: port, host: host, log: logFile.Name(), exited: make(chan struct{})}
	// The child holds its own descriptors for these; the parent's copy is only
	// needed until Start, and leaving it open leaks one per stack operation.
	go func() { _ = cmd.Wait(); logFile.Close(); close(p.exited) }() // sole reaper; stop() only observes
	// Poll until the port answers. Budget is generous — a cold proxy opening a
	// large --local cache is slower than josh-sync's 1s assumption.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		dialCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", "127.0.0.1:"+strconv.Itoa(port))
		cancel()
		if err == nil {
			conn.Close()
			return p, nil
		}
		select {
		case <-p.exited:
			// Its log is the only account of why it gave up.
			err := fmt.Errorf("josh-proxy exited before becoming ready (port %d)", port)
			if tail := p.tail(15); tail != "" {
				err = fmt.Errorf("%w\n--- josh-proxy log:\n%s", err, tail)
			}
			p.cleanup()
			return nil, err
		case <-time.After(10 * time.Millisecond):
		}
	}
	p.stop()
	return nil, fmt.Errorf("josh-proxy did not become ready on port %d", port)
}

// cleanup removes the engine's log once nothing more will be read from it.
// Called after stop, or on a startup path that never handed the proxy back.
func (p *joshProxy) cleanup() {
	if p.log != "" {
		_ = os.Remove(p.log)
		p.log = ""
	}
}

// stop shuts the proxy down, gracefully first so its --local cache is left
// consistent for the next invocation.
func (p *joshProxy) stop() {
	select {
	case <-p.exited:
		p.cleanup() // already gone, but its log is still ours to remove
		return
	default:
	}
	// os.Interrupt is not implemented on Windows: sending it there fails, and
	// waiting out the grace period would add two seconds to every operation for
	// a signal the process never received. Kill is the only option that platform
	// gives us, so take it immediately rather than after a pointless wait.
	if runtime.GOOS == "windows" {
		_ = p.cmd.Process.Kill()
		<-p.exited
		p.cleanup()
		return
	}
	_ = p.cmd.Process.Signal(os.Interrupt)
	select {
	case <-p.exited:
	case <-time.After(2 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.exited
	}
	p.cleanup()
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
	// PathEscape, not QueryEscape: this is a path segment, where QueryEscape's
	// space-as-plus would rename a prefix to a directory literally called "+".
	return fmt.Sprintf("%s%s.git%s%s.git", p.base(), repoPath, at, url.PathEscape(filter))
}

// base is the proxy's root URL, for scoping git config — notably the
// Authorization header — to this proxy and nothing else.
func (p *joshProxy) base() string {
	return fmt.Sprintf("http://127.0.0.1:%d/", p.port)
}

// tail returns the last lines of the engine's log, for attaching to an error.
func (p *joshProxy) tail(lines int) string {
	data, err := os.ReadFile(p.log)
	if err != nil {
		return ""
	}
	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n")
}

// stackPrefixFilter is the josh filter that maps a whole upstream repo under a
// workspace prefix (and, reversed on push, back out of it).
func stackPrefixFilter(prefix string) string { return ":prefix=" + prefix }

// freePort asks the kernel for an unused port and hands it to the child. There
// is an unavoidable gap between closing this listener and the child binding —
// josh-proxy takes a port, not a socket — so callers retry rather than treat a
// bind failure as fatal.
func freePort(ctx context.Context) (int, error) {
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
