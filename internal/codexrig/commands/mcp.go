package commands

import (
	"fmt"
	"github.com/rigsmith/rigsmith/internal/agentrig/redact"
	"net/url"
	"sort"
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
				return writeJSON(out, map[string]any{"servers": forDisplay(entries)})
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
				fmt.Fprintf(out, "%-18s %-9s %-9s %s\n", e.Name, e.Transport(), travels, DimStyle.Render(displaySummary(e.Server)))
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
				fmt.Fprintf(out, "  %-10s %s\n", "target", displaySummary(e.Server))
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

// forDisplay is what the listing may say about a server. This is a
// portability report, not a config viewer: it names which env keys will be
// stripped, and never needs their values — and a token in argv or a
// user:password@ in a URL is exactly the kind of thing that ends up pasted
// into an issue from here. The keys stay; the values do not.
func forDisplay(entries []mcp.Entry) []displayEntry {
	out := make([]displayEntry, 0, len(entries))
	for _, e := range entries {
		keys := make([]string, 0, len(e.Env))
		for k := range e.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out = append(out, displayEntry{
			Name: e.Name, Transport: string(e.Transport()), Enabled: e.Enabled,
			Target: displaySummary(e.Server), Cwd: e.Cwd, EnvKeys: keys,
			Portability: e.Portability,
		})
	}
	return out
}

type displayEntry struct {
	Name        string          `json:"name"`
	Transport   string          `json:"transport"`
	Enabled     bool            `json:"enabled"`
	Target      string          `json:"target"`
	Cwd         string          `json:"cwd,omitempty"`
	EnvKeys     []string        `json:"envKeys,omitempty"`
	Portability mcp.Portability `json:"portability"`
}

// displaySummary is Summary with credentials taken out: a URL loses its
// userinfo, and an argument that looks like a secret is shown as such.
func displaySummary(s mcp.Server) string {
	if s.URL != "" {
		// Scheme, host and path only. Userinfo is the obvious place for a
		// credential; a query string or fragment is the other, and a listing
		// has no use for either.
		u, err := url.Parse(s.URL)
		if err != nil {
			return "(unparseable url)"
		}
		shown := u.Scheme + "://"
		if u.User != nil {
			shown += "***@"
		}
		shown += u.Host + u.Path
		if u.RawQuery != "" || u.Fragment != "" {
			shown += "?…"
		}
		return shown
	}
	args := make([]string, 0, len(s.Args))
	for _, a := range s.Args {
		v := a
		if eq := strings.IndexByte(a, '='); eq > 0 && strings.HasPrefix(a, "-") {
			v = a[eq+1:]
		}
		if _, ok := redact.LooksSecret(v); ok {
			a = strings.TrimSuffix(a, v) + "<redacted>"
		}
		args = append(args, a)
	}
	return strings.TrimSpace(s.Command + " " + strings.Join(args, " "))
}
