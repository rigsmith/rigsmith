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

// stackJoshChecksums pins what each platform's josh-proxy must hash to. Pinned
// here rather than read from the release, so that trusting a download does not
// reduce to trusting whoever can write to the release. Regenerate with
// `rig stack doctor --print-checksums` when the release moves.
var stackJoshChecksums = map[string]string{
	"linux-arm64":   "685728fae9346cbbcda43ad372b02d447f873e8121f12de1403fac02b8533763",
	"linux-x64":     "845c717891965242ce716f88efdec76eb0ed96e45ecc63933ef2f6d544d2638d",
	"macos-arm64":   "f353cf4c845152347bd2bf645065267d0b48096af5b802f8abefcfdba565ac74",
	"macos-x64":     "c659519ba3aa855053e03adbe723bee5de88c6c9850a95775b4de83d6065a172",
	"windows-arm64": "c96ef9eb891c2b270f65d6fea3d62cdf43628674dac4801ad359e3af699121b9",
	"windows-x64":   "2ae71b33b80cfc4fac70f0e1e6ac4878671237630e628fc9b86eebb6d8f149ee",
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

// downloadJoshProxy fetches the pinned engine for this platform into dest. It
// verifies the checksum when one is pinned, and refuses a download it cannot
// check rather than trusting the bytes.
func downloadJoshProxy(ctx context.Context, dest string, out io.Writer) error {
	target, err := stackJoshTarget()
	if err != nil {
		return err
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	asset := fmt.Sprintf("josh-proxy-%s-%s%s", stackJoshRelease, target, ext)
	url := fmt.Sprintf("%s/%s/%s", stackJoshBinaries, stackJoshRelease, asset)

	want, pinned := stackJoshChecksums[target]
	if !pinned {
		return fmt.Errorf("no checksum is pinned for %s, so %s cannot be verified", target, asset)
	}

	fmt.Fprintf(out, "fetching josh-proxy %s for %s\n", stackJoshRelease, target)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
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
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".josh-proxy-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	sum := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, sum), resp.Body); err != nil {
		tmp.Close()
		return err
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
	// A published binary is seconds; building josh is minutes. Only fall back
	// to cargo when this platform has no binary, or the download fails.
	if version == stackJoshVersion {
		if err := downloadJoshProxy(ctx, bin, out); err == nil {
			return bin, nil
		} else {
			fmt.Fprintf(out, "could not fetch a published josh-proxy (%v); building from source\n", err)
		}
	}

	cargo, err := exec.LookPath("cargo")
	if err != nil {
		return "", fmt.Errorf("josh-proxy %s is not installed, no published binary could be fetched, and cargo was not found; install rust (https://rustup.rs) and re-run", version)
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
	log    string        // where the engine's own output went, for diagnosing failures
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
	// git writes this path into .git/objects/info/alternates, where a colon
	// separates entries: a host carrying a port would be split in half and the
	// fetch would fail claiming the repository is corrupt.
	local := filepath.Join(cache, "rigsmith", "josh", strings.ReplaceAll(host, ":", "_"))
	if err := os.MkdirAll(local, 0o755); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin,
		"--local", local,
		"--remote", stackRemoteScheme(host)+host,
		fmt.Sprintf("--port=%d", port),
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

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
