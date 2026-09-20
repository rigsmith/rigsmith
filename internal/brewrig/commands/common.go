package commands

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/rigsmith/rigsmith/internal/brewrig/brew"
	"github.com/rigsmith/rigsmith/internal/brewrig/config"
	"github.com/rigsmith/rigsmith/internal/brewrig/engine"
	"github.com/rigsmith/rigsmith/internal/brewrig/inventory"
	"github.com/rigsmith/rigsmith/internal/brewrig/plan"
	"github.com/rigsmith/rigsmith/internal/brewrig/store"
)

// interactive reports whether we're attached to a terminal (so it's safe to
// prompt). A variable so a test can pin the answer: a prompt a test reaches on
// a developer's terminal blocks on stdin instead of asserting anything.
var interactive = Interactive

// Interactive reports whether both stdin and stdout are real terminals — the
// shared gate for any prompt and for landing bare `brewrig` on the dashboard.
func Interactive() bool {
	return isatty.IsTerminal(os.Stdout.Fd()) && isatty.IsTerminal(os.Stdin.Fd())
}

// session is the common setup every verb but `init` needs: config, a brew
// client, and the local clone brought up to date.
type session struct {
	cfg *config.Config
	// brew is the quiet client used for reads. mut streams brew's output to
	// stderr and is used only for installing, removing and upgrading.
	//
	// They are separate because a read must not be noisy: Homebrew emits
	// deprecation warnings from third-party taps on ordinary commands, and
	// with one shared streaming client those land in the middle of every
	// status report.
	brew  *brew.Client
	mut   *brew.Client
	store *store.Store
}

// open loads the config, checks brew is usable and syncs the clone.
//
// pull is false for the verbs that only read local state, so `brewrig status`
// still answers on a plane.
func open(ctx context.Context, pull bool) (*session, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	c := brew.New()
	if err := c.Available(ctx); err != nil {
		return nil, err
	}
	dir, err := config.StagingDir()
	if err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, dir, cfg.Remote, cfg.BranchOrDefault())
	if err != nil {
		return nil, err
	}
	if pull {
		if err := st.Pull(ctx); err != nil {
			return nil, err
		}
	}
	return &session{cfg: cfg, brew: c, mut: brew.NewStreaming(), store: st}, nil
}

// snapshot builds this machine's current inventory on top of what it published
// last time, which is what turns a vanished package into a recorded retirement.
func (s *session) snapshot(ctx context.Context, now time.Time) (*inventory.Machine, []inventory.Ref, error) {
	prev, _, err := s.store.Load(s.cfg.Machine)
	if err != nil {
		return nil, nil, err
	}
	return engine.Snapshot(ctx, s.brew, s.cfg.Machine, config.OSToken(), prev, now)
}

// planNow snapshots this machine and builds its plan against every published
// machine, substituting the fresh snapshot for this machine's stored file so
// the plan reflects reality rather than the last sync.
func (s *session) planNow(ctx context.Context, now time.Time) (*inventory.Machine, *plan.Plan, error) {
	self, _, err := s.snapshot(ctx, now)
	if err != nil {
		return nil, nil, err
	}
	all, err := s.store.Machines(ctx)
	if err != nil {
		return nil, nil, err
	}
	merged := make([]*inventory.Machine, 0, len(all)+1)
	seen := false
	for _, m := range all {
		if m.Name == self.Name {
			merged = append(merged, self)
			seen = true
			continue
		}
		merged = append(merged, m)
	}
	if !seen {
		merged = append(merged, self)
	}
	return self, plan.Build(self, merged), nil
}

// stepPrinter reports each brew action as it starts.
func stepPrinter() engine.Progress {
	return func(action, name string) {
		fmt.Fprintf(os.Stderr, "%s %s %s\n", AccentStyle.Render("→"), action, name)
	}
}

// reportResult prints what an apply did and returns an error when anything
// failed, so the process exit code reflects a partial run.
func reportResult(res *engine.Result) error {
	for _, t := range res.Tapped {
		fmt.Printf("  %s tapped %s\n", OkStyle.Render("✓"), t)
	}
	for _, r := range res.Installed {
		fmt.Printf("  %s installed %s\n", OkStyle.Render("✓"), r.Label())
	}
	for _, r := range res.Removed {
		fmt.Printf("  %s removed %s\n", OkStyle.Render("✓"), r.Label())
	}
	for _, f := range res.Failed {
		name := f.Tap
		if name == "" {
			name = f.Ref.Label()
		}
		fmt.Printf("  %s %s: %v\n", ErrStyle.Render("✗"), name, f.Err)
	}
	if n := len(res.Failed); n > 0 {
		return fmt.Errorf("%d of %d actions failed", n, n+len(res.Tapped)+len(res.Installed)+len(res.Removed))
	}
	return nil
}

// optOut records that this machine deliberately declines a package, and
// publishes the decision so the other machines stop proposing it.
//
// It is published rather than kept local for a reason: a purely local skip list
// means the other machine keeps offering the package forever, and the only
// visible effect is a prompt you learn to dismiss.
func (s *session) optOut(ctx context.Context, r inventory.Ref) error {
	self, _, err := s.snapshot(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	if self.OptedOut(r) {
		return nil
	}
	switch r.Kind {
	case inventory.Cask:
		self.OptOut.Casks = append(self.OptOut.Casks, r.Name)
	default:
		self.OptOut.Formulae = append(self.OptOut.Formulae, r.Name)
	}
	if err := s.store.Write(self); err != nil {
		return err
	}
	_, err = s.store.Publish(ctx, fmt.Sprintf("%s: skip %s", self.Name, r.Label()))
	return err
}

// nowUTC is the clock the commands read, as a variable so a test can pin it.
var nowUTC = func() time.Time { return time.Now().UTC() }
