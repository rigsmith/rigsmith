package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/stale"
	"github.com/spf13/cobra"
)

// `rig verify` does two jobs, and the second is the valuable one.
//
// Sequencing: build, then test, then run, stopping at the first failure — so
// "I checked" means one thing instead of three.
//
// Agreement: sequencing alone doesn't solve the problem it looks like it
// solves, it hides it, by rebuilding everything every time. That is fine for a
// Go service and unusable where a build is minutes to hours — exactly the
// repos where stale artifacts survive longest, because nobody rebuilds
// casually. So verify also compares what each verb produces against what the
// next consumes and says so plainly when a consumer is older than its
// producer, without rebuilding to find out (see internal/rig/stale).
//
// Both halves fail loudly. A check that exits zero while being wrong is worse
// than no check: a warning buried in a long log is what got missed the first
// time.

// newVerifyCmd builds the `verify` command.
func newVerifyCmd() *cobra.Command {
	var (
		staleOnly  bool
		noRun      bool
		runTimeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Build, test and run in sequence, then check the artifacts agree",
		Long: strings.TrimSpace(`
Run build, then test, then run — stopping at the first failure — and finish by
checking that the artifacts in play were actually built from the code you have.

That last check is the point. Each verb answers its own question honestly, and
the answers can still be collectively wrong: a test binary two hours older than
the resources it loads, an app bundle with a fresh library beside a stale one.
verify compares modification times rather than rebuilding, so the answer costs a
second even where a build costs hours.

  rig verify              build → test → run, then the agreement check
  rig verify --stale-only report disagreement, run nothing
  rig verify --no-run     build and test only

Without configuration, verify asks the generic question every ecosystem
supports: is anything under the source tree newer than the newest build output?
Artifacts rig cannot infer — generated resources, multi-artifact builds, an
out/ tree beside the repo — are declared in .rig.json:

  "artifacts": {
    "browser":    { "path": "../out/Release/App.app", "inputs": ["**/*.cc", "**/*.grd"] },
    "unit-tests": { "path": "../out/Release/unit_tests", "inputs": ["**/*.cc", "**/*.h"] }
  }

Checks that could not run are always reported as skipped, never counted as
passes — silence about a check that did not run is how a green result becomes
misleading. Exits non-zero on any failure, so it can gate CI or a pre-push
hook.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, _ := os.Getwd()
			root := resolveRoot(cwd)
			// Unlike every other verb, verify does not shrug off an unreadable
			// config: a .rig.json it could not load is an `artifacts` block it
			// silently dropped, and "artifacts agree" would then be a claim
			// about checks that never happened.
			cfg, err := config.LoadMerged(root)
			if err != nil {
				return fmt.Errorf("could not load %s: %w", config.FileName, err)
			}
			// A file that loaded but didn't parse degrades to defaults with a
			// warning — same problem, so say that out loud too.
			reportConfigWarnings(cmd, cfg)

			timeout, err := cfg.VerifyRunTimeout()
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("run-timeout") {
				// The config path rejects a non-positive timeout; the flag has
				// to as well, or `--run-timeout 0` would launch a server with
				// no deadline at all and hang instead of being bounded.
				if runTimeout <= 0 {
					return fmt.Errorf("--run-timeout must be positive, got %s", runTimeout)
				}
				timeout = runTimeout
			}

			if staleOnly {
				return reportAgreement(cmd, root, cfg)
			}
			for _, verb := range []string{"build", "test"} {
				verifyStep(cmd, verb, "")
				if err := runVerifyVerb(cmd, verb, 0); err != nil {
					return fmt.Errorf("%s failed — stopping here: %w", verb, err)
				}
			}
			switch {
			case noRun:
				verifySkipped(cmd, "run", "--no-run")
			case !cfg.VerifyRun():
				verifySkipped(cmd, "run", "verify.run is false in "+config.FileName)
			default:
				verifyStep(cmd, "run", fmt.Sprintf("passes if still alive after %s", timeout))
				if err := runVerifyVerb(cmd, "run", timeout); err != nil {
					return fmt.Errorf("run failed — stopping here: %w", err)
				}
			}
			verifyStep(cmd, "agreement", "")
			return reportAgreement(cmd, root, cfg)
		},
	}
	cmd.Flags().BoolVar(&staleOnly, "stale-only", false, "only check that the artifacts agree — run nothing")
	cmd.Flags().BoolVar(&noRun, "no-run", false, "skip the run step (build and test only)")
	cmd.Flags().DurationVar(&runTimeout, "run-timeout", config.DefaultVerifyRunTimeout,
		"how long the run step must stay alive to count as started")
	return cmd
}

// reportConfigWarnings surfaces the config loader's non-fatal complaints
// (a malformed .rig.json degraded to defaults, an unknown key) before any
// check runs.
func reportConfigWarnings(cmd *cobra.Command, cfg config.Config) {
	for _, w := range cfg.Warnings {
		fmt.Fprintln(cmd.ErrOrStderr(), warnStyle.Render("! "+w))
	}
}

// verifyStep prints the step header so a long log stays readable — which verb
// is running, and (for run) what counts as passing.
func verifyStep(cmd *cobra.Command, name, note string) {
	line := "· " + name
	if note != "" {
		line += " (" + note + ")"
	}
	fmt.Fprintln(cmd.OutOrStdout(), dimStyle.Render(line))
}

// verifySkipped prints a skipped step and why. A step that did not run is
// always said out loud, for the same reason a skipped check is.
func verifySkipped(cmd *cobra.Command, name, why string) {
	fmt.Fprintln(cmd.OutOrStdout(), dimStyle.Render(fmt.Sprintf("· %s — skipped (%s)", name, why)))
}

// runVerifyVerb runs one dev verb through the SAME command the standalone verb
// uses. verify must never resolve a target differently from `rig build` /
// `rig test` / `rig run` — if the two could disagree about what they run,
// verify would be worse than useless — so it reuses the verb rather than
// reproducing its resolution.
//
// A positive timeout applies to the run step: a server or a desktop app never
// exits on its own, so "still alive when the clock runs out" is the answer we
// were looking for ("does it start"), not a failure.
func runVerifyVerb(cmd *cobra.Command, verb string, timeout time.Duration) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
		// Own the whole process tree for the bounded launch. Killing just the
		// direct child leaves its descendants running — `go run .` execs the
		// binary it compiled, `npm run dev` spawns a server — so verify would
		// report "it starts" and walk away from a live process still holding
		// the port. Scoped to this step: the other verbs stay in rig's own
		// process group so terminal signals reach them as usual.
		prev := isolateProcessTree
		isolateProcessTree = true
		defer func() { isolateProcessTree = prev }()
	}
	sub := verifyStepCmd(verb)
	sub.SetContext(ctx)
	sub.SetOut(cmd.OutOrStdout())
	sub.SetErr(cmd.ErrOrStderr())
	err := sub.RunE(sub, nil)
	if timeout > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil // survived the timeout — it starts
	}
	return err
}

// verifyStepCmd builds the dev-verb command for a step, with the same shape the
// root tree registers (build/test take --all, run doesn't).
func verifyStepCmd(verb string) *cobra.Command {
	if verb == "run" {
		return devVerbCmd("run", "Run the project", false, "dev")
	}
	return devVerbCmd(verb, "", true)
}

// reportAgreement runs the staleness checks and prints them, returning an error
// when anything is stale so the exit code gates CI. Staleness is a failure, not
// a warning: a warning in a long log is what got missed the first time.
func reportAgreement(cmd *cobra.Command, root string, cfg config.Config) error {
	findings := agreementFindings(root, cfg)
	staleCount, skipped := renderFindings(cmd.OutOrStdout(), findings)
	if staleCount > 0 {
		return fmt.Errorf("%s stale — the artifacts do not agree with the source; rebuild before trusting this result",
			countNoun(staleCount, "check is", "checks are"))
	}
	// A report where nothing ran must not read as agreement — that is the
	// misleading green this whole verb exists to prevent. It isn't a failure
	// either (absent config is not an error), so it says what happened and
	// leaves the exit code alone.
	if ran := len(findings) - skipped; ran == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), warnStyle.Render(fmt.Sprintf(
			"verify: nothing could be checked — %s could not run", countNoun(skipped, "check", "checks"))))
		return nil
	}
	summary := fmt.Sprintf("verify: artifacts agree (%s)", countNoun(len(findings)-skipped, "check", "checks"))
	if skipped > 0 {
		summary += fmt.Sprintf(", %s could not run", countNoun(skipped, "check", "checks"))
	}
	fmt.Fprintln(cmd.OutOrStdout(), okStyle.Render(summary))
	return nil
}

// agreementFindings collects every staleness check for the repo. An unresolved
// primary ecosystem doesn't abort the report: the generic check is reported as
// skipped (with the reason) and the declared artifacts — which need no
// ecosystem — are still checked.
func agreementFindings(root string, cfg config.Config) []stale.Finding {
	artifacts := verifyArtifacts(cfg)
	cwd, _ := os.Getwd()
	eco, err := resolvePrimary(cwd, root)
	if err != nil {
		skipped := stale.Finding{Name: stale.OutputCheckName, Status: stale.Skipped, Reason: err.Error()}
		return append([]stale.Finding{skipped}, stale.CheckArtifacts(root, artifacts)...)
	}
	return stale.Check(root, eco, artifacts)
}

// verifyArtifacts converts the config's `artifacts` block into the checker's
// input. Absent config is not an error — it yields no declared artifacts, and
// verify falls back to the generic checks.
func verifyArtifacts(cfg config.Config) []stale.Artifact {
	out := make([]stale.Artifact, 0, len(cfg.Artifacts))
	for name, a := range cfg.Artifacts {
		if a == nil {
			continue
		}
		out = append(out, stale.Artifact{Name: name, Path: a.Path, Inputs: a.Inputs})
	}
	return out
}

// renderFindings prints one aligned line per check and returns how many were
// stale and how many could not run.
func renderFindings(out io.Writer, findings []stale.Finding) (staleCount, skipped int) {
	width := 0
	for _, f := range findings {
		if n := len(f.Name); n > width {
			width = n
		}
	}
	for _, f := range findings {
		var mark, detail string
		switch f.Status {
		case stale.Stale:
			staleCount++
			mark, detail = failStyle.Render("✗"), f.Detail()
		case stale.Skipped:
			skipped++
			mark, detail = dimStyle.Render("·"), "skipped — "+f.Detail()
		default:
			mark, detail = okStyle.Render("✓"), f.Detail()
		}
		line := fmt.Sprintf("  %s %s", mark, pad(f.Name, width))
		if detail != "" {
			line += "  " + dimStyle.Render(detail)
		}
		fmt.Fprintln(out, strings.TrimRight(line, " "))
	}
	return staleCount, skipped
}

// countNoun renders "1 check" / "3 checks".
func countNoun(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
