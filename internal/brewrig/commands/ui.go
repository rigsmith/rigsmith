package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/internal/brewrig/config"
	"github.com/spf13/cobra"
)

// NewUICmd builds `brewrig ui`, the dashboard a bare `brewrig` lands on.
//
// It leads with the current situation rather than a bare menu: the useful
// question on opening this is "are the machines in sync", and answering it
// first means the menu is a follow-up rather than a quiz.
func NewUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "The interactive dashboard",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			if _, err := config.Load(); err != nil {
				fmt.Fprintf(out, "%s %v\n\n", WarnStyle.Render("!"), err)
				return climenu.RunMenu(cmd.Parent(), "brewrig", "not set up on this machine yet", []climenu.Entry{
					{Label: "init", Desc: "set this machine up", Cmd: find(cmd.Parent(), "init")},
					{Label: "doctor", Desc: "check what's missing", Cmd: find(cmd.Parent(), "doctor")},
				})
			}

			summary := headline(ctx, out)
			return climenu.RunMenu(cmd.Parent(), "brewrig", summary, []climenu.Entry{
				{Label: "status", Desc: "what differs between the machines", Cmd: find(cmd.Parent(), "status")},
				{Label: "apply", Desc: "install what's missing here", Cmd: find(cmd.Parent(), "apply")},
				{Label: "sync", Desc: "publish this machine", Cmd: find(cmd.Parent(), "sync")},
				{Label: "update", Desc: "upgrade and publish", Cmd: find(cmd.Parent(), "update")},
				{Label: "doctor", Desc: "health-check the setup", Cmd: find(cmd.Parent(), "doctor")},
			})
		},
	}
}

// headline is the one-line situation shown above the menu. It is best-effort:
// the dashboard must still open when the remote is down, so any failure
// degrades to a plain description instead of an error.
func headline(ctx context.Context, out interface{ Write([]byte) (int, error) }) string {
	s, err := open(ctx, true)
	if err != nil {
		return "couldn't reach the shared repo — local actions still work"
	}
	_, p, err := s.planNow(ctx, time.Now().UTC())
	if err != nil {
		return "on " + s.cfg.Machine
	}
	switch {
	case len(p.Install) > 0 && len(p.Remove) > 0:
		return fmt.Sprintf("%s: %d missing, %d removed elsewhere", s.cfg.Machine, len(p.Install), len(p.Remove))
	case len(p.Install) > 0:
		return fmt.Sprintf("%s: %d package(s) missing here", s.cfg.Machine, len(p.Install))
	case len(p.Remove) > 0:
		return fmt.Sprintf("%s: %d package(s) removed on another machine", s.cfg.Machine, len(p.Remove))
	case len(p.Skew) > 0:
		return fmt.Sprintf("%s: same packages, %d on different versions", s.cfg.Machine, len(p.Skew))
	default:
		return s.cfg.Machine + ": in sync"
	}
}

// find looks up a sibling verb by name so the menu dispatches the real command
// rather than a reimplementation of it.
func find(parent *cobra.Command, name string) *cobra.Command {
	if parent == nil {
		return nil
	}
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}
