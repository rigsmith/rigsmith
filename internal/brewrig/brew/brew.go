// Package brew is the only part of brewrig that talks to Homebrew. Everything
// else works on the inventory model, so the logic that decides to uninstall
// software can be tested without a machine to break.
package brew

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/brewrig/inventory"
)

// Runner executes one brew invocation. Tests substitute a fake; the real one is
// Exec.
type Runner interface {
	Run(ctx context.Context, args ...string) (stdout []byte, err error)
}

// Exec runs the real brew binary.
type Exec struct {
	// Bin is the brew executable; empty means "brew" from PATH.
	Bin string
	// Stream, when set, receives brew's stderr live. Installs are slow and
	// chatty and a silent ten-minute pause reads as a hang, so the commands
	// that change something pass os.Stderr here.
	Stream *os.File
}

// Run executes brew and returns stdout only.
//
// stdout and stderr are kept apart deliberately. Homebrew writes deprecation
// warnings from third-party taps to stderr during ordinary reads — this machine
// emits one on every single command from a tap it has installed — and folding
// them into stdout would put that text straight into the parsed package list.
func (e Exec) Run(ctx context.Context, args ...string) ([]byte, error) {
	bin := e.Bin
	if bin == "" {
		bin = "brew"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	if e.Stream != nil {
		cmd.Stderr = e.Stream
	} else {
		cmd.Stderr = &errb
	}
	// HOMEBREW_NO_AUTO_UPDATE keeps a read from silently turning into a
	// multi-minute tap refresh; brewrig updates only when told to, in `update`.
	// NO_ENV_HINTS drops the advertising footer from parsed output.
	cmd.Env = append(os.Environ(), "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ENV_HINTS=1")
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg != "" {
			return out.Bytes(), fmt.Errorf("brew %s: %w: %s", strings.Join(args, " "), err, lastLines(msg, 8))
		}
		return out.Bytes(), fmt.Errorf("brew %s: %w", strings.Join(args, " "), err)
	}
	return out.Bytes(), nil
}

// lastLines trims a brew error down to its tail, which is where the actual
// cause is; the preceding output is usually progress.
func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return "…\n" + strings.Join(lines[len(lines)-n:], "\n")
}

// Client is the brew-facing API the rest of brewrig uses.
type Client struct{ R Runner }

// New returns a Client over the real brew binary.
func New() *Client { return &Client{R: Exec{}} }

// NewStreaming returns a Client whose brew output is echoed to stderr, for the
// commands that install or upgrade.
func NewStreaming() *Client { return &Client{R: Exec{Stream: os.Stderr}} }

// ErrNotInstalled is returned when brew is not on this machine.
var ErrNotInstalled = errors.New("Homebrew is not installed (or not on PATH) — see https://brew.sh")

// Available reports whether brew can be found and run.
func (c *Client) Available(ctx context.Context) error {
	if _, err := c.R.Run(ctx, "--version"); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrNotInstalled
		}
		var ee *exec.Error
		if errors.As(err, &ee) {
			return ErrNotInstalled
		}
		return err
	}
	return nil
}

// infoV2 is the slice of `brew info --json=v2 --installed` brewrig reads. The
// full document is large and mostly irrelevant; naming only these fields keeps
// the parse resilient to everything else Homebrew adds.
type infoV2 struct {
	Formulae []struct {
		Name      string `json:"name"`
		FullName  string `json:"full_name"`
		Tap       string `json:"tap"`
		Installed []struct {
			Version            string `json:"version"`
			InstalledOnRequest bool   `json:"installed_on_request"`
			Time               int64  `json:"time"`
		} `json:"installed"`
	} `json:"formulae"`
	Casks []struct {
		Token     string `json:"token"`
		FullToken string `json:"full_token"`
		Tap       string `json:"tap"`
		Version   string `json:"version"`
		Installed string `json:"installed"`
		// InstalledTime is absent on older Homebrew; a zero value simply means
		// this cask cannot win a retire-vs-reinstall race on time alone.
		InstalledTime int64 `json:"installed_time"`
	} `json:"casks"`
}

// Inventory reads what this machine has deliberately installed.
//
// It is one `brew info --json=v2 --installed` call — taps, versions and install
// times all arrive together, in well under a second — plus `brew tap` for taps
// that carry no installed package yet.
func (c *Client) Inventory(ctx context.Context, machine, osName string) (*inventory.Machine, error) {
	raw, err := c.R.Run(ctx, "info", "--json=v2", "--installed")
	if err != nil {
		return nil, err
	}
	var doc infoV2
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parsing brew info output: %w", err)
	}

	m := &inventory.Machine{
		Schema:    inventory.SchemaVersion,
		Name:      machine,
		OS:        osName,
		Arch:      runtime.GOARCH,
		ChangedAt: time.Now().UTC(),
	}

	m.Present = map[string]bool{}
	for _, f := range doc.Formulae {
		if len(f.Installed) == 0 {
			continue
		}
		inst := f.Installed[0]
		m.Present[inventory.Ref{Kind: inventory.Formula, Name: nameFor(f.Name, f.FullName, f.Tap)}.String()] = true
		// The dependency closure is deliberately not published: only what was
		// asked for. See docs/BREWRIG-DESIGN.md.
		if !inst.InstalledOnRequest {
			continue
		}
		m.Formulae = append(m.Formulae, inventory.Package{
			Name:        nameFor(f.Name, f.FullName, f.Tap),
			Tap:         f.Tap,
			Version:     inst.Version,
			InstalledAt: inst.Time,
		})
	}
	for _, k := range doc.Casks {
		if k.Installed == "" {
			continue
		}
		m.Present[inventory.Ref{Kind: inventory.Cask, Name: nameFor(k.Token, k.FullToken, k.Tap)}.String()] = true
		m.Casks = append(m.Casks, inventory.Package{
			Name:        nameFor(k.Token, k.FullToken, k.Tap),
			Tap:         k.Tap,
			Version:     k.Installed,
			InstalledAt: k.InstalledTime,
		})
	}

	taps, err := c.Taps(ctx)
	if err != nil {
		return nil, err
	}
	m.Taps = taps

	if v, err := c.R.Run(ctx, "--version"); err == nil {
		m.BrewVersion = firstLineVersion(string(v))
	}
	if p, err := c.R.Run(ctx, "--prefix"); err == nil {
		m.Prefix = strings.TrimSpace(string(p))
	}

	m.Normalize()
	return m, nil
}

// nameFor prefers the tap-qualified name for a third-party package, because
// `brew install depot` and `brew install depot/tap/depot` are not the same
// request on a machine that has not tapped it.
func nameFor(short, full, tap string) string {
	if full != "" && tap != "" && tap != "homebrew/core" && tap != "homebrew/cask" {
		return full
	}
	if short != "" {
		return short
	}
	return full
}

func firstLineVersion(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(strings.TrimPrefix(line, "Homebrew "))
}

// Taps lists the taps configured on this machine.
func (c *Client) Taps(ctx context.Context) ([]string, error) {
	out, err := c.R.Run(ctx, "tap")
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(string(out)), nil
}

// Outdated lists installed packages with a newer version available. It does not
// refresh the tap metadata first — call Update for that — so it reports against
// what this machine last fetched.
func (c *Client) Outdated(ctx context.Context) ([]inventory.Ref, error) {
	var refs []inventory.Ref
	out, err := c.R.Run(ctx, "outdated", "--formula", "--quiet")
	if err != nil {
		return nil, err
	}
	for _, n := range nonEmptyLines(string(out)) {
		refs = append(refs, inventory.Ref{Kind: inventory.Formula, Name: n})
	}
	out, err = c.R.Run(ctx, "outdated", "--cask", "--quiet")
	if err != nil {
		return nil, err
	}
	for _, n := range nonEmptyLines(string(out)) {
		refs = append(refs, inventory.Ref{Kind: inventory.Cask, Name: n})
	}
	inventory.SortRefs(refs)
	return refs, nil
}

// Tap adds a tap.
func (c *Client) Tap(ctx context.Context, name string) error {
	_, err := c.R.Run(ctx, "tap", name)
	return err
}

// Install installs one package.
func (c *Client) Install(ctx context.Context, r inventory.Ref) error {
	_, err := c.R.Run(ctx, "install", kindFlag(r), r.Name)
	return err
}

// Uninstall removes one package.
func (c *Client) Uninstall(ctx context.Context, r inventory.Ref) error {
	_, err := c.R.Run(ctx, "uninstall", kindFlag(r), r.Name)
	return err
}

// Update refreshes Homebrew itself and its tap metadata.
func (c *Client) Update(ctx context.Context) error {
	_, err := c.R.Run(ctx, "update")
	return err
}

// Upgrade upgrades everything outdated. greedy also upgrades casks that
// auto-update themselves, which brew otherwise leaves alone — those are the
// ones most likely to drift between two machines, but upgrading them can
// restart a running app, so it stays opt-in.
func (c *Client) Upgrade(ctx context.Context, greedy bool) error {
	args := []string{"upgrade"}
	if greedy {
		args = append(args, "--greedy")
	}
	_, err := c.R.Run(ctx, args...)
	return err
}

// kindFlag disambiguates the two namespaces. Without it `brew install docker`
// is ambiguous — there is both a formula and a cask by that name — and brew
// picks for you.
func kindFlag(r inventory.Ref) string {
	if r.Kind == inventory.Cask {
		return "--cask"
	}
	return "--formula"
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
