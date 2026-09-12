package commands

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/journal"
	"github.com/rigsmith/rigsmith/internal/codexrig/status"
	"github.com/rigsmith/rigsmith/internal/codexrig/tui"
	"github.com/spf13/cobra"
)

// NewUICmd builds `codexrig ui`, the dashboard a bare `codexrig` lands on.
func NewUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "The interactive dashboard",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if recs, rerr := journal.Read(staging, 1); rerr == nil && len(recs) > 0 {
				last, haveLast = recs[0], true
			}

			model, err := tea.NewProgram(tui.New(info, status.Judge(info, last, haveLast))).Run()
			if err != nil {
				return err
			}
			chosen := model.(tui.Model).Chosen
			if chosen == "" {
				return nil
			}
			// Dispatched AFTER the event loop, so the command owns the terminal
			// rather than competing with bubbletea for it.
			return dispatch(cmd, chosen)
		},
	}
}

func dispatch(cmd *cobra.Command, verb string) error {
	var next *cobra.Command
	switch verb {
	case "init":
		next = NewInitCmd()
	case "sync":
		next = NewSyncCmd()
	case "restore":
		next = NewRestoreCmd()
	case "status":
		next = NewStatusCmd()
	case "doctor":
		next = NewDoctorCmd(cmd.Root().Version)
	case "account":
		next = NewAccountCmd()
	default:
		return nil
	}
	next.SetContext(cmd.Context())
	next.SetOut(cmd.OutOrStdout())
	next.SetErr(cmd.ErrOrStderr())
	next.SetIn(cmd.InOrStdin())
	// Not nil: Cobra resolves SetArgs(nil) to os.Args[1:], which still holds
	// "ui" — and the dispatched command's cobra.NoArgs then rejects its own
	// name. An empty non-nil slice means what nil was meant to mean.
	next.SetArgs([]string{})
	return next.Execute()
}
