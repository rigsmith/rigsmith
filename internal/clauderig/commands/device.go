package commands

import (
	"errors"
	"fmt"

	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/spf13/cobra"
)

// NewDeviceCmd builds the `device` group — the synced registry of machines
// sharing this repo.
//
// It exists mostly for cleanup. The registry had no way to remove an entry, so
// a device named "this" — registered before hostname detection worked —
// survived in the synced file from June 2026 until it was deleted by hand.
// Registration now refuses an unresolved name (see sync), and this is how the
// leftovers get cleared.
func NewDeviceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "device",
		Aliases: []string{"devices"},
		Short:   "Inspect and clean up the synced device registry",
		Long: "The machines sharing this sync repo, as recorded in clauderig-devices.json.\n\n" +
			"  list    show every registered machine and when it last synced\n" +
			"  remove  drop a machine from the registry (it re-registers if it syncs again)",
		// Bare `device` on a TTY opens the subcommand menu; off a TTY it prints
		// help, so scripts and `-h` are unchanged.
		RunE: func(cmd *cobra.Command, args []string) error {
			if Interactive() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newDeviceListCmd(), newDeviceRemoveCmd())
	return cmd
}

func newDeviceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Show every machine in the synced registry",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			reg, me, err := loadRegistry()
			if err != nil {
				return err
			}

			list := reg.List()
			if len(list) == 0 {
				fmt.Fprintln(out, DimStyle.Render("no devices registered yet — run `clauderig sync`"))
				return nil
			}
			fmt.Fprintln(out, HeaderStyle.Render("clauderig devices"))
			for _, d := range list {
				self := ""
				if d.Name == me {
					self = DimStyle.Render(" (this)")
				}
				fmt.Fprintf(out, "  %-24s %-8s %-10s %s%s\n", d.Name, d.OS,
					orDash(d.ClaudeVersion), DimStyle.Render(humanizeSince(d.LastSync)), self)
			}
			return nil
		},
	}
}

func newDeviceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Drop a machine from the synced registry",
		Long: "Remove a machine's entry from clauderig-devices.json. The registry is synced,\n" +
			"so the removal reaches the other machines on the next sync. This deletes no\n" +
			"session data and does not stop that machine syncing — if it syncs again it\n" +
			"simply re-registers. Requires an interactive terminal.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			name := args[0]

			reg, me, err := loadRegistry()
			if err != nil {
				return err
			}
			if !reg.Has(name) {
				return fmt.Errorf("no device named %q in the registry (see `clauderig device list`)", name)
			}
			if !Interactive() {
				return errors.New("refusing to remove a device without a terminal to confirm")
			}

			prompt := fmt.Sprintf("Remove %s from the synced device registry?", name)
			if name == me {
				// Removing yourself is legal but pointless — the next sync puts
				// it straight back. Say so rather than letting it look broken.
				prompt = fmt.Sprintf("%s is THIS machine; it will re-register on the next sync. Remove anyway?", name)
			}
			ok, err := confirmDestructive(prompt)
			if err != nil || !ok {
				fmt.Fprintln(out, DimStyle.Render("aborted"))
				return err
			}

			staging, err := config.StagingDir()
			if err != nil {
				return err
			}
			reg.Remove(name)
			if err := reg.Save(staging); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("Removed"), name)
			fmt.Fprintln(out, DimStyle.Render("Run `clauderig sync` to push the change to your other machines."))
			return nil
		},
	}
}

// loadRegistry reads the synced registry plus this machine's name, so callers
// can mark which row is the local one.
func loadRegistry() (*devices.Registry, string, error) {
	cfg, err := config.LoadOrDefault()
	if err != nil {
		return nil, "", err
	}
	staging, err := config.StagingDir()
	if err != nil {
		return nil, "", err
	}
	reg, err := devices.Load(staging)
	if err != nil {
		return nil, "", err
	}
	return reg, config.ResolveName(cfg), nil
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
