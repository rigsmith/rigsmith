package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/rigsmith/rigsmith/internal/agentrig/ghrepo"
	"github.com/rigsmith/rigsmith/internal/brewrig/brew"
	"github.com/rigsmith/rigsmith/internal/brewrig/config"
	"github.com/rigsmith/rigsmith/internal/brewrig/store"
	"github.com/spf13/cobra"
)

// NewDoctorCmd health-checks this machine's setup.
func NewDoctorCmd(version string) *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that brewrig can do its job on this machine",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd.Context(), cmd.OutOrStdout(), version, fix)
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "repair what can be repaired (currently: a broken local clone)")
	return cmd
}

type check struct {
	name   string
	detail string
	status string // ok | warn | fail
	hint   string
}

func runDoctor(ctx context.Context, out io.Writer, version string, fix bool) error {
	var checks []check
	add := func(c check) { checks = append(checks, c) }

	add(check{name: "brewrig", detail: version, status: "ok"})

	c := brew.New()
	if err := c.Available(ctx); err != nil {
		add(check{name: "homebrew", detail: "not usable", status: "fail", hint: err.Error()})
	} else {
		add(check{name: "homebrew", detail: "available", status: "ok"})
	}

	if _, err := exec.LookPath("git"); err != nil {
		add(check{name: "git", detail: "not found", status: "fail", hint: "install git"})
	} else {
		add(check{name: "git", detail: "available", status: "ok"})
	}

	if _, err := exec.LookPath("gh"); err != nil {
		add(check{name: "gh", detail: "not installed", status: "warn",
			hint: "needed only to create or verify a private repo: brew install gh"})
	} else {
		add(check{name: "gh", detail: "available", status: "ok"})
	}

	cfg, err := config.Load()
	switch {
	case err != nil:
		// No hint: the error text already ends with the command to run, and
		// repeating it on the next line reads as two different instructions.
		add(check{name: "config", detail: err.Error(), status: "fail"})
	default:
		add(check{name: "config", detail: "machine " + cfg.Machine, status: "ok"})
		if cfg.Remote == "" {
			add(check{name: "remote", detail: "none configured", status: "fail", hint: "run `brewrig init`"})
		} else if !store.Reachable(ctx, cfg.Remote) {
			add(check{name: "remote", detail: ghrepo.SafeRemote(cfg.Remote), status: "fail", hint: "check network / gh auth status"})
		} else {
			add(check{name: "remote", detail: ghrepo.SafeRemote(cfg.Remote), status: "ok"})
			// Privacy is verified at `init`, but a repo can be flipped to
			// public afterwards and nothing would notice. Reachable is not the
			// same as still private, and a package list is a decent map of the
			// machine — so re-verify here, where a slow network call is the
			// point of the command.
			// "Cannot verify" and "verified public" are different answers and
			// must not share a verdict. A local path is not a public repo, and
			// saying so would be confidently wrong — but the reverse matters
			// more: a repo that IS public must not be softened into a warning.
			//
			// Which is why this asks the error and not the URL. Gating on
			// ParseSlug looked equivalent and was not: it is GitHub-only, so
			// every gitlab.com remote took the "cannot verify" path and a
			// verifiably public GitLab repo was reported as a warning.
			switch err := ghrepo.EnsurePrivate(ctx, cfg.Remote); {
			case err == nil:
				add(check{name: "remote privacy", detail: "private", status: "ok"})
			case errors.Is(err, ghrepo.ErrUnsupportedRemote):
				add(check{name: "remote privacy", detail: "not a github.com or gitlab.com remote", status: "warn",
					hint: "privacy cannot be verified here; make sure it is not readable by anyone else"})
			case errors.Is(err, ghrepo.ErrVerifierUnavailable):
				add(check{name: "remote privacy", detail: "not verified", status: "warn", hint: err.Error()})
			default:
				add(check{name: "remote privacy", detail: err.Error(), status: "fail",
					hint: "a public repo would publish your package list; make it private again"})
			}
		}

		if dir, derr := config.StagingDir(); derr == nil {
			st, serr := store.Open(ctx, dir, cfg.Remote, cfg.BranchOrDefault())
			// The clone holds no unique state — everything in it came from the
			// remote — so throwing it away and re-cloning is a safe repair,
			// and it is the only one brewrig has to offer.
			if serr != nil && fix {
				if rmErr := os.RemoveAll(dir); rmErr == nil {
					st, serr = store.Open(ctx, dir, cfg.Remote, cfg.BranchOrDefault())
					if serr == nil {
						add(check{name: "clone", detail: "re-cloned", status: "ok"})
					}
				}
			}
			if serr != nil {
				hint := "it is recreated by the next sync"
				if !fix {
					hint = "run `brewrig doctor --fix` to re-clone it"
				}
				add(check{name: "clone", detail: serr.Error(), status: "warn", hint: hint})
			} else if all, merr := st.Machines(ctx); merr != nil {
				add(check{name: "inventories", detail: merr.Error(), status: "fail"})
			} else {
				d := fmt.Sprintf("%d machine(s) published", len(all))
				status := "ok"
				hint := ""
				if len(all) < 2 {
					status, hint = "warn", "run `brewrig init` on your other Mac"
				}
				add(check{name: "inventories", detail: d, status: status, hint: hint})
			}
		}
	}

	failed := false
	for _, c := range checks {
		mark := OkStyle.Render("✓")
		switch c.status {
		case "warn":
			mark = WarnStyle.Render("!")
		case "fail":
			mark = ErrStyle.Render("✗")
			failed = true
		}
		fmt.Fprintf(out, "%s %-13s %s\n", mark, c.name, c.detail)
		if c.hint != "" {
			fmt.Fprintf(out, "  %s\n", DimStyle.Render(c.hint))
		}
	}
	checkFailed = failed
	// Deliberately no error return. doctor has already rendered the problem in
	// full; returning an error would make fang print a second, empty ERROR
	// block underneath it. main consults CheckFailed for the exit code instead.
	return nil
}

// checkFailed records that a check command found a problem, so the process can
// exit non-zero without printing anything more.
var checkFailed bool

// CheckFailed reports whether a check command found a problem this run.
func CheckFailed() bool { return checkFailed }
