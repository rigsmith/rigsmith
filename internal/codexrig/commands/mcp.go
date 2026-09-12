package commands

import (
	"fmt"
	"strings"

	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/mcp"
	"github.com/spf13/cobra"
)

// NewMCPCmd builds `codexrig mcp` — a READ-ONLY view of your MCP servers, from
// the one angle Codex cannot give you: what happens to them on another machine.
//
// Adding and removing servers is `codex mcp`'s job. A second writer for one TOML
// table is only a way for the two to disagree.
func NewMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Show which of your MCP servers survive a restore",
		Long: "`codex mcp` configures servers. This says what a restore will do to them.\n\n" +
			"Two things stop a server travelling, and they need different fixes: a value in\n" +
			"its [env] table is treated as a secret and redacted, so the other machine has\n" +
			"to supply its own; and an absolute path outside your home cannot be made\n" +
			"portable, so it arrives spelled for this machine.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && interactive() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newMCPListCmd(), newMCPGetCmd())
	return cmd
}

func loadServers() ([]mcp.Entry, error) {
	home, err := codexhome.Default()
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadOrDefault()
	if err != nil {
		return nil, err
	}
	me := config.DetectFor(cfg)
	return mcp.List(home, me.Folders(), me.OS)
}

func newMCPListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the configured MCP servers and their portability",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			entries, err := loadServers()
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(out, map[string]any{"servers": entries})
			}
			if len(entries) == 0 {
				fmt.Fprintln(out, DimStyle.Render("no MCP servers configured — `codex mcp add`"))
				return nil
			}
			fmt.Fprintf(out, "%-18s %-9s %-9s %s\n", HeaderStyle.Render("NAME"), "TRANSPORT", "TRAVELS", "TARGET")
			for _, e := range entries {
				travels := OkStyle.Render("yes")
				if !e.Portability.Portable() {
					travels = WarnStyle.Render("needs work")
				}
				if !e.Enabled {
					travels = DimStyle.Render("disabled")
				}
				fmt.Fprintf(out, "%-18s %-9s %-9s %s\n", e.Name, e.Transport(), travels, DimStyle.Render(e.Summary()))
				printNeeds(cmd, e)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the listing as JSON")
	return cmd
}

func newMCPGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Show one server in full",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			entries, err := loadServers()
			if err != nil {
				return err
			}
			for _, e := range entries {
				if e.Name != args[0] {
					continue
				}
				fmt.Fprintf(out, "%s\n", HeaderStyle.Render(e.Name))
				fmt.Fprintf(out, "  %-10s %s\n", "transport", e.Transport())
				fmt.Fprintf(out, "  %-10s %s\n", "target", e.Summary())
				if e.Cwd != "" {
					fmt.Fprintf(out, "  %-10s %s\n", "cwd", e.Cwd)
				}
				if len(e.Env) > 0 {
					keys := make([]string, 0, len(e.Env))
					for k := range e.Env {
						keys = append(keys, k)
					}
					// Names only. The values are exactly what this tool exists
					// not to print.
					fmt.Fprintf(out, "  %-10s %s\n", "env", strings.Join(keys, ", "))
				}
				printNeeds(cmd, e)
				return nil
			}
			return fmt.Errorf("no MCP server named %q", args[0])
		},
	}
}

func printNeeds(cmd *cobra.Command, e mcp.Entry) {
	out := cmd.OutOrStdout()
	if len(e.Portability.SecretEnv) > 0 {
		fmt.Fprintf(out, "    %s %s\n", DimStyle.Render("set again on each machine:"), strings.Join(e.Portability.SecretEnv, ", "))
	}
	if len(e.Portability.LocalPaths) > 0 {
		fmt.Fprintf(out, "    %s %s\n", DimStyle.Render("machine-specific path in:"), strings.Join(e.Portability.LocalPaths, ", "))
	}
}
