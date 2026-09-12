package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/devices"
	"github.com/rigsmith/rigsmith/internal/codexrig/engine"
	"github.com/rigsmith/rigsmith/internal/codexrig/journal"
	"github.com/rigsmith/rigsmith/internal/codexrig/service"
	"github.com/spf13/cobra"
)

// NewSyncCmd builds `codexrig sync`.
func NewSyncCmd() *cobra.Command {
	var dryRun, hook, flush bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Capture this machine's Codex setup and push it",
		Long: "Walks your Codex home, redacts anything credential-shaped, rewrites machine\n" +
			"paths into portable ones, and commits the result to your own private repo.\n\n" +
			"Nothing is pushed if the tripwire finds a credential the redactor did not\n" +
			"catch — a refused sync is the tool working, and the reason is recorded in the\n" +
			"journal so a hook-driven run has somewhere to say so.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			staging, err := config.StagingDir()
			if err != nil {
				return err
			}
			me := config.DetectFor(cfg)

			// A hook-driven run, or any run without a terminal, is automated:
			// it debounces and it gives up the lock rather than queueing.
			automated := hook || !interactive()
			wait := time.Duration(0)
			if flush || !automated {
				wait = flushLockWait
			}
			lock, got, err := acquireSyncLock(staging, wait)
			if err != nil {
				return err
			}
			defer lock.Release()
			if !got {
				if automated && !flush {
					fmt.Fprintf(out, "  %s\n", DimStyle.Render("another sync is running — skipping"))
					return nil
				}
				return fmt.Errorf("another sync is still running; retry after it finishes")
			}

			if automated && !flush {
				if every := cfg.HookInterval(); every > 0 {
					if last, ok := journal.LastSuccessful(staging, me.Name, journal.OpSync); ok {
						if since := time.Since(last.At); since < every {
							fmt.Fprintf(out, "  %s\n", DimStyle.Render(fmt.Sprintf(
								"synced %s ago — next in %s (hookIntervalMinutes)",
								humanize(since), humanize(every-since))))
							return nil
						}
					}
				}
			}

			var flushPaths []string
			if flush {
				flushPaths = hookRollouts(cmd.InOrStdin(), cmd.ErrOrStderr())
			}

			svc := service.Service{
				Observe:      renderEvent(out),
				ReadIdentity: readIdentity,
			}
			res, serr := svc.Sync(cmd.Context(), service.SyncRequest{
				Config:       cfg,
				Machine:      me,
				StagingDir:   staging,
				CodexVersion: detectCodexVersion(),
				DryRun:       dryRun,
				Flush:        flushPaths,
			})
			if res.Capture != nil {
				printSyncSummary(out, res.Capture)
			}
			return serr
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "capture into staging but do not commit or push")
	cmd.Flags().BoolVar(&hook, "hook", false, "this run came from a Codex hook: debounce it and never block")
	cmd.Flags().BoolVar(&flush, "flush", false, "capture the rollout named on stdin now, bypassing the large-file throttle")
	return cmd
}

// hookRollouts decodes a Codex hook payload from stdin and returns the rollout
// it names.
//
// Three outcomes, and the difference matters. A path means flush that session
// alone. NO payload at all — a person typing the command — means flush
// everything, because they asked for it deliberately. An unreadable payload
// means flush NOTHING and say so: guessing "everything" there would restage
// every large rollout whenever a hook misfired, which is the exact growth the
// throttle exists to prevent.
func hookRollouts(in io.Reader, errOut io.Writer) []string {
	if f, ok := in.(*os.File); ok && (isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())) {
		return nil
	}
	type payload struct {
		// Codex names the file under several keys depending on the event; all
		// of them mean the same thing.
		RolloutPath    string `json:"rollout_path"`
		TranscriptPath string `json:"transcript_path"`
		SessionFile    string `json:"session_file"`
	}
	done := make(chan []string, 1)
	go func() {
		var p payload
		if err := json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&p); err != nil {
			fmt.Fprintf(errOut, "  %s\n", WarnStyle.Render("hook payload on stdin could not be read; flushing nothing"))
			done <- []string{}
			return
		}
		for _, v := range []string{p.RolloutPath, p.TranscriptPath, p.SessionFile} {
			if strings.TrimSpace(v) != "" {
				done <- []string{v}
				return
			}
		}
		fmt.Fprintf(errOut, "  %s\n", WarnStyle.Render("hook payload names no rollout; flushing nothing"))
		done <- []string{}
	}()
	select {
	case paths := <-done:
		return paths
	case <-time.After(2 * time.Second):
		fmt.Fprintf(errOut, "  %s\n", WarnStyle.Render("nothing arrived on stdin within 2s; flushing nothing"))
		return []string{}
	}
}

// renderEvent turns the service's progress into terminal lines.
func renderEvent(out io.Writer) func(service.Event) {
	return func(e service.Event) {
		switch ev := e.(type) {
		case service.MergePending:
			fmt.Fprintf(out, "  %s\n", DimStyle.Render("settling a merge left from an earlier run"))
		case service.ConflictsResolved:
			if len(ev.Report.Resolved) > 0 || len(ev.Report.Unresolved) > 0 {
				fmt.Fprint(out, ev.Report.Describe())
			}
		case service.Published:
			switch {
			case ev.LocalOnly:
				fmt.Fprintf(out, "  %s\n", DimStyle.Render("committed locally — no remote configured (`codexrig config set remote <url>`)"))
			case ev.Pushed:
				fmt.Fprintf(out, "  %s\n", OkStyle.Render("pushed"))
			}
		case service.PullFailed:
			fmt.Fprintf(out, "  %s %v\n", WarnStyle.Render("!"), ev.Err)
		case service.Restored:
			fmt.Fprintf(out, "  %s %d file(s)\n", OkStyle.Render("restored"), ev.Report.Written())
		}
	}
}

func printSyncSummary(out io.Writer, rep *engine.Report) {
	for _, rr := range rep.Roots {
		if rr.Absent {
			fmt.Fprintf(out, "  %-6s %s\n", rr.ID, DimStyle.Render("absent on this machine"))
			continue
		}
		parts := []string{fmt.Sprintf("%d written", rr.Files), fmt.Sprintf("%d unchanged", rr.Unchanged)}
		if rr.Redactions > 0 {
			parts = append(parts, fmt.Sprintf("%d redacted", rr.Redactions))
		}
		if rr.Deferred > 0 {
			parts = append(parts, fmt.Sprintf("%d deferred", rr.Deferred))
		}
		if rr.AgedOut > 0 {
			parts = append(parts, fmt.Sprintf("%d aged out", rr.AgedOut))
		}
		if rr.Disallowed > 0 {
			parts = append(parts, fmt.Sprintf("%d retired", rr.Disallowed))
		}
		if rr.Skipped > 0 {
			parts = append(parts, fmt.Sprintf("%d skipped", rr.Skipped))
		}
		fmt.Fprintf(out, "  %-6s %s\n", rr.ID, strings.Join(parts, " · "))
		for _, o := range rr.Oversize {
			fmt.Fprintf(out, "    %s %s (%s)\n", WarnStyle.Render("too large:"), o.Rel, humanBytes(o.Bytes))
		}
	}
	for _, f := range rep.Findings {
		fmt.Fprintf(out, "  %s %s (%s)\n", ErrStyle.Render("credential:"), f.Path, f.Kind)
	}
}

// readIdentity reports the login this machine syncs as — identity only, because
// this value is written into the synced device registry.
func readIdentity() (devices.Account, error) {
	raw, err := accountReadLive()
	if err != nil {
		return devices.Account{}, err
	}
	id := accountIdentityOf(raw)
	return devices.Account{Email: id.Email, AccountID: id.AccountID}, nil
}

// detectCodexVersion asks the Codex CLI what it is. Best-effort: a machine
// without codex on PATH still syncs, it just cannot stamp a version.
func detectCodexVersion() string {
	bin, err := exec.LookPath("codex")
	if err != nil {
		return ""
	}
	// Bounded: this runs on every sync, including the hook-driven ones that
	// must never block, and a codex that does not return would stall them all.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return ""
	}
	// "codex-cli 0.144.6"
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

func humanize(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

func humanBytes(n int64) string {
	const unit = 1 << 10
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
