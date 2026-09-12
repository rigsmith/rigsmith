package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/codexrig/account"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/journal"
	"github.com/rigsmith/rigsmith/internal/codexrig/status"
	"github.com/spf13/cobra"
)

// statusJSON is the snapshot plus the judgement. A thin wrapper on purpose: the
// screen reads the struct and --json marshals the same one, so the two can never
// drift into telling different stories.
type statusJSON struct {
	status.Info
	Level   string          `json:"level"`
	Reason  string          `json:"reason"`
	Summary string          `json:"summary"`
	Action  string          `json:"action,omitempty"`
	LastRun *journal.Record `json:"lastRun,omitempty"`
}

// NewStatusCmd builds `codexrig status`.
func NewStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "How the sync is doing on this machine",
		Args:  cobra.NoArgs,
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
			info := status.Gather(cmd.Context(), cfg, me, staging, gatherAccount())

			var last journal.Record
			haveLast := false
			if recs, err := journal.Read(staging, 1); err == nil && len(recs) > 0 {
				last, haveLast = recs[0], true
			}
			rep := status.Judge(info, last, haveLast)

			if asJSON {
				doc := statusJSON{Info: info, Level: rep.Level.String(), Reason: rep.Reason, Summary: rep.Summary, Action: rep.Action}
				if haveLast {
					doc.LastRun = &last
				}
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(doc)
			}
			printStatus(cmd.Context(), out, info, rep, last, haveLast)
			return nil
		},
	}
	// --json deliberately skips the reachability probe below: it is for scripts
	// and pollers, and a hung remote must not hang them.
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the snapshot as JSON (for scripts and pollers)")
	return cmd
}

func printStatus(ctx context.Context, out io.Writer, info status.Info, rep status.Report, last journal.Record, haveLast bool) {
	fmt.Fprintf(out, "%s  %s (%s)\n", HeaderStyle.Render("codexrig"), info.Machine.Name, info.Machine.OS)

	remote := DimStyle.Render("none — run `codexrig init`")
	if info.Remote != "" {
		remote = info.Remote
		// Only on the human path, and bounded: the JSON path skips it entirely.
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		if !gitrepo.Reachable(probe, info.Remote) {
			remote += " " + WarnStyle.Render("(unreachable)")
		}
		cancel()
	}
	fmt.Fprintf(out, "  %-11s %s\n", "remote", remote)

	lastSync := DimStyle.Render("never")
	if info.LastSync != "" {
		lastSync = info.LastSync
	}
	fmt.Fprintf(out, "  %-11s %s\n", "last sync", lastSync)
	if info.Dirty {
		fmt.Fprintf(out, "  %-11s %s\n", "", WarnStyle.Render("uncommitted changes in the staging repo"))
	}
	if info.Unpushed > 0 {
		fmt.Fprintf(out, "  %-11s %s\n", "", WarnStyle.Render(fmt.Sprintf("%d commit(s) never pushed", info.Unpushed)))
	}
	if info.Unmerged > 0 {
		fmt.Fprintf(out, "  %-11s %s\n", "", DimStyle.Render(fmt.Sprintf("%d on the remote not here yet", info.Unmerged)))
	}

	if info.Account.LoggedOut {
		fmt.Fprintf(out, "  %-11s %s\n", "login", DimStyle.Render("not logged in — run `codex login`"))
	} else if info.Account.Email != "" {
		line := info.Account.Email
		if info.Account.Plan != "" {
			line += DimStyle.Render(" · " + info.Account.Plan)
		}
		fmt.Fprintf(out, "  %-11s %s\n", "login", line)
	}
	if info.Account.Problem != "" {
		fmt.Fprintf(out, "  %-11s %s\n", "", WarnStyle.Render(info.Account.Problem))
	}

	fmt.Fprintf(out, "  %s\n", "roots:")
	for _, r := range info.Roots {
		if !r.Present {
			fmt.Fprintf(out, "    %-8s %s\n", r.ID, DimStyle.Render("absent here"))
			continue
		}
		fmt.Fprintf(out, "    %-8s %d files\n", r.ID, r.Files)
	}
	sessions := DimStyle.Render("config only — rollouts are not being backed up (`codexrig config set syncSessions true`)")
	if info.Sessions {
		sessions = "config and session rollouts"
	}
	fmt.Fprintf(out, "  %-11s %s\n", "carrying", sessions)

	if len(info.Devices) > 0 {
		fmt.Fprintf(out, "  %s\n", "devices:")
		for _, d := range info.Devices {
			mark := ""
			if d.Name == info.Machine.Name {
				mark = DimStyle.Render(" (this)")
			}
			fmt.Fprintf(out, "    %-14s %-8s %s%s\n", d.Name, d.OS, humanSince(d.LastSync), mark)
		}
	}

	fmt.Fprintln(out)
	style := OkStyle
	switch rep.Level {
	case status.Amber:
		style = WarnStyle
	case status.Red:
		style = ErrStyle
	}
	fmt.Fprintf(out, "  %s %s\n", style.Render("●"), rep.Summary)
	if rep.Action != "" {
		fmt.Fprintf(out, "    %s\n", DimStyle.Render("→ "+rep.Action))
	}
	if haveLast && !last.OK() {
		fmt.Fprintf(out, "    %s\n", DimStyle.Render("last run: "+last.Summary()))
	}
}

// gatherAccount reads the live login for the status line. Best-effort: a status
// command that fails because nobody is logged in is a status command that cannot
// report being logged out.
func gatherAccount() status.AccountInfo {
	raw, err := account.ReadLive()
	if err != nil {
		return status.AccountInfo{LoggedOut: true}
	}
	id := account.IdentityOf(raw)
	info := status.AccountInfo{Email: id.Email, Plan: id.PlanType}
	if !account.HasTokens(raw) {
		info.Problem = "the credential holds no usable token — run `codex login`"
	}
	if s, serr := account.DefaultStore(); serr == nil {
		if a, rerr := s.Resolve(id.Email); rerr == nil {
			info.Alias = a.Alias
		}
		// Only when nothing worse is already reported. "The credential holds no
		// usable token" is the more urgent of the two and was being overwritten
		// by the drift note, which is advice about a login that does not work.
		if o := s.Diagnose(); o.PointerEmail != "" && info.Problem == "" {
			info.Problem = "codexrig's active account (" + o.PointerEmail + ") is not the login in use"
		}
	}
	return info
}

func humanSince(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
