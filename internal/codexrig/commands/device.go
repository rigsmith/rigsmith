package commands

import (
	"errors"
	"fmt"

	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/devices"
	"github.com/spf13/cobra"
)

// NewDeviceCmd builds `codexrig device`: the machines syncing into this repo.
func NewDeviceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "device",
		Short: "The machines syncing into this repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && interactive() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newDeviceListCmd(), newDeviceForgetCmd())
	return cmd
}

func newDeviceListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List every machine and when it last synced",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			reg, me, err := loadDevices()
			if err != nil {
				return err
			}
			list := reg.List()
			if asJSON {
				return writeJSON(out, map[string]any{"this": me, "devices": list})
			}
			if len(list) == 0 {
				fmt.Fprintln(out, DimStyle.Render("no machines registered yet — run `codexrig sync`"))
				return nil
			}
			fmt.Fprintln(out, HeaderStyle.Render("Machines"))
			for _, d := range list {
				mark := ""
				if d.Name == me {
					mark = DimStyle.Render(" (this)")
				}
				line := fmt.Sprintf("  %-16s %-8s %s%s", d.Name, d.OS, humanSince(d.LastSync), mark)
				fmt.Fprintln(out, line)
				var detail []string
				if d.CodexVersion != "" {
					detail = append(detail, "codex "+d.CodexVersion)
				}
				if d.Account != nil && d.Account.Email != "" {
					detail = append(detail, d.Account.Email)
				}
				if len(detail) > 0 {
					fmt.Fprintf(out, "    %s\n", DimStyle.Render(joinDetail(detail)))
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the listing as JSON")
	return cmd
}

func newDeviceForgetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "forget <name>",
		Short: "Remove a machine from the registry",
		Long: "For a computer you no longer have. It removes the registry entry only —\n" +
			"whatever that machine synced stays in the repo, because it is your data and\n" +
			"forgetting the machine is not the same as wanting the work gone.\n\n" +
			"The entry comes back if that machine syncs again.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			staging, err := config.StagingDir()
			if err != nil {
				return err
			}
			reg, me, err := loadDevices()
			if err != nil {
				return err
			}
			if args[0] == me {
				return errors.New("that is this machine; forgetting it would only make the next sync register it again")
			}
			if !reg.Remove(args[0]) {
				return fmt.Errorf("no machine named %q is registered", args[0])
			}
			if err := reg.Save(staging); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("forgot"), args[0])
			fmt.Fprintf(out, "  %s\n", DimStyle.Render("its synced files are untouched"))
			return nil
		},
	}
}

func loadDevices() (*devices.Registry, string, error) {
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

func joinDetail(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " · "
		}
		out += p
	}
	return out
}
