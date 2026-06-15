package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/clauderig/mcp"
	"github.com/rigsmith/rigsmith/internal/clauderig/settings"
	"github.com/rigsmith/rigsmith/internal/clauderig/tui"
	"github.com/spf13/cobra"
)

// NewMCPCmd builds the `mcp` command group: manage Claude Code MCP servers across
// the user / project / local scopes by editing the canonical files directly
// (~/.claude.json and <repo>/.mcp.json). Bare `clauderig mcp` on a TTY opens the
// interactive screen; with a verb or off a TTY the subcommands stand.
func NewMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage Claude Code MCP servers (list, add, remove, enable, disable)",
		Long: "Manage Claude Code MCP servers across scopes, editing the canonical files\n" +
			"directly:\n" +
			"  user     ~/.claude.json    mcpServers                  (every project)\n" +
			"  project  <repo>/.mcp.json  mcpServers                  (committed, shared)\n" +
			"  local    ~/.claude.json    projects[<repo>].mcpServers (this checkout)\n\n" +
			"Bare `clauderig mcp` opens the interactive screen.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if Interactive() {
				return runMCPUI(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newMCPListCmd(),
		newMCPGetCmd(),
		newMCPAddCmd(),
		newMCPRemoveCmd(),
		newMCPEnableCmd(),
		newMCPDisableCmd(),
	)
	return cmd
}

// mcpHomeRepo resolves the home dir and (best-effort) repo root the mcp commands
// operate on. repo is "" when not inside a git repository.
func mcpHomeRepo(ctx context.Context) (home, repo string, err error) {
	home, err = os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	return home, repoRootBestEffort(ctx), nil
}

// scopeFlag adds the shared --scope flag (defaulting to user) and returns a
// resolver that parses it.
func scopeFlag(cmd *cobra.Command, def settings.Scope) func() (settings.Scope, error) {
	var raw string
	cmd.Flags().StringVarP(&raw, "scope", "s", string(def), "scope: user | project | local")
	return func() (settings.Scope, error) { return settings.Parse(raw) }
}

func newMCPListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List configured MCP servers across scopes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, repo, err := mcpHomeRepo(cmd.Context())
			if err != nil {
				return err
			}
			entries, err := mcp.List(home, repo)
			if err != nil {
				return err
			}
			return printServerList(cmd, entries, repo == "")
		},
	}
	return cmd
}

func newMCPGetCmd() *cobra.Command {
	var resolve func() (settings.Scope, error)
	cmd := &cobra.Command{
		Use:   "get <name>",
		Short: "Show one MCP server's configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := resolve()
			if err != nil {
				return err
			}
			home, repo, err := mcpHomeRepo(cmd.Context())
			if err != nil {
				return err
			}
			srv, ok, err := mcp.Get(scope, home, repo, args[0])
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("no %s-scope server named %q", scope, args[0])
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s %s\n", HeaderStyle.Render(args[0]), DimStyle.Render("("+string(scope)+")"))
			fmt.Fprintf(out, "  transport  %s\n", srv.Transport())
			if srv.Command != "" {
				fmt.Fprintf(out, "  command    %s\n", srv.Command)
			}
			if len(srv.Args) > 0 {
				fmt.Fprintf(out, "  args       %s\n", strings.Join(srv.Args, " "))
			}
			if srv.URL != "" {
				fmt.Fprintf(out, "  url        %s\n", srv.URL)
			}
			for k, v := range srv.Env {
				fmt.Fprintf(out, "  env        %s=%s\n", k, v)
			}
			for k, v := range srv.Headers {
				fmt.Fprintf(out, "  header     %s=%s\n", k, v)
			}
			return nil
		},
	}
	resolve = scopeFlag(cmd, settings.User)
	return cmd
}

func newMCPAddCmd() *cobra.Command {
	var resolve func() (settings.Scope, error)
	var transport string
	var env, headers []string
	cmd := &cobra.Command{
		Use:   "add <name> <command|url> [args...]",
		Short: "Add (or replace) an MCP server",
		Long: "Add an MCP server. clauderig's own flags (--scope, --transport, --env,\n" +
			"--header) come before <name>; everything after is the server's command/url\n" +
			"and its args, so a server's own flags (e.g. `npx -y pkg`) pass through.\n\n" +
			"stdio (the default) — give the command and its args:\n" +
			"  clauderig mcp add --env KEY=val ctx7 npx -y @upstash/context7-mcp\n" +
			"http/sse — give the URL:\n" +
			"  clauderig mcp add -t http -H Authorization=Bearer... docs https://example.com/mcp",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := resolve()
			if err != nil {
				return err
			}
			home, repo, err := mcpHomeRepo(cmd.Context())
			if err != nil {
				return err
			}
			name := args[0]
			srv, err := buildServer(transport, args[1:], env, headers)
			if err != nil {
				return err
			}
			if _, exists, _ := mcp.Get(scope, home, repo, name); exists {
				fmt.Fprintf(cmd.OutOrStdout(), "%s replacing existing %s-scope server %q\n", WarnStyle.Render("!"), scope, name)
			}
			if err := mcp.Add(scope, home, repo, name, srv); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s added %s %s\n", OkStyle.Render("✓"), name, DimStyle.Render("("+string(scope)+")"))
			return nil
		},
	}
	resolve = scopeFlag(cmd, settings.User)
	cmd.Flags().StringVarP(&transport, "transport", "t", mcp.TransportStdio, "transport: stdio | http | sse")
	cmd.Flags().StringArrayVarP(&env, "env", "e", nil, "environment variable KEY=VALUE (repeatable)")
	cmd.Flags().StringArrayVarP(&headers, "header", "H", nil, "HTTP header KEY=VALUE for http/sse (repeatable)")
	// Stop parsing flags at the first positional so a server's own flags (e.g.
	// `npx -y pkg`) pass through as args. clauderig's own flags go before <name>.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func newMCPRemoveCmd() *cobra.Command {
	var resolve func() (settings.Scope, error)
	cmd := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove an MCP server",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := resolve()
			if err != nil {
				return err
			}
			home, repo, err := mcpHomeRepo(cmd.Context())
			if err != nil {
				return err
			}
			removed, err := mcp.Remove(scope, home, repo, args[0])
			if err != nil {
				return err
			}
			if !removed {
				fmt.Fprintf(cmd.OutOrStdout(), "%s no %s-scope server named %q\n", DimStyle.Render("•"), scope, args[0])
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s removed %s %s\n", OkStyle.Render("✓"), args[0], DimStyle.Render("("+string(scope)+")"))
			return nil
		},
	}
	resolve = scopeFlag(cmd, settings.User)
	return cmd
}

func newMCPEnableCmd() *cobra.Command  { return newMCPToggleCmd("enable", true) }
func newMCPDisableCmd() *cobra.Command { return newMCPToggleCmd("disable", false) }

// newMCPToggleCmd builds the enable/disable commands, which record a project
// (.mcp.json) server's approval in your local settings.json.
func newMCPToggleCmd(verb string, enabled bool) *cobra.Command {
	title := strings.ToUpper(verb[:1]) + verb[1:]
	return &cobra.Command{
		Use:   verb + " <name>",
		Short: title + " a project (.mcp.json) MCP server for this machine",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, repo, err := mcpHomeRepo(cmd.Context())
			if err != nil {
				return err
			}
			if repo == "" {
				return fmt.Errorf("%s applies to project servers — run inside a git repository", verb)
			}
			if err := mcp.SetEnabled(home, repo, args[0], enabled); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %sd %s %s\n", OkStyle.Render("✓"), verb, args[0], DimStyle.Render("(local settings)"))
			return nil
		},
	}
}

// buildServer assembles a Server from the add command's transport, positional
// target/args, and env/header flags.
func buildServer(transport string, rest, env, headers []string) (mcp.Server, error) {
	srv := mcp.Server{Type: transport}
	envMap, err := parseKV(env)
	if err != nil {
		return srv, err
	}
	switch transport {
	case mcp.TransportStdio, "":
		if len(rest) == 0 {
			return srv, fmt.Errorf("stdio server needs a command")
		}
		srv.Type = mcp.TransportStdio
		srv.Command = rest[0]
		srv.Args = rest[1:]
		srv.Env = envMap
	case mcp.TransportHTTP, mcp.TransportSSE:
		if len(rest) == 0 {
			return srv, fmt.Errorf("%s server needs a URL", transport)
		}
		srv.URL = rest[0]
		hdrs, err := parseKV(headers)
		if err != nil {
			return srv, err
		}
		srv.Headers = hdrs
		srv.Env = envMap
	default:
		return srv, fmt.Errorf("unknown transport %q (want stdio | http | sse)", transport)
	}
	return srv, nil
}

// parseKV turns ["K=V", ...] into a map, erroring on a missing "=".
func parseKV(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("expected KEY=VALUE, got %q", p)
		}
		m[k] = v
	}
	return m, nil
}

// printServerList renders the `mcp list` table.
func printServerList(cmd *cobra.Command, entries []mcp.Entry, noRepo bool) error {
	out := cmd.OutOrStdout()
	if len(entries) == 0 {
		fmt.Fprintln(out, DimStyle.Render("no MCP servers configured — add one with `clauderig mcp add`"))
		return nil
	}
	fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf("%-8s %-18s %-9s %-9s %s", "SCOPE", "NAME", "TRANSPORT", "STATE", "TARGET")))
	for _, e := range entries {
		fmt.Fprintf(out, "%-8s %-18s %-9s %-9s %s\n",
			string(e.Scope), e.Name, e.Server.Transport(),
			stateText(e.State), DimStyle.Render(e.Server.Summary()))
	}
	if noRepo {
		fmt.Fprintln(out, DimStyle.Render("(not in a repo — only user-scope servers shown)"))
	}
	return nil
}

func stateText(s mcp.State) string {
	switch s {
	case mcp.StateEnabled:
		return OkStyle.Render("enabled")
	case mcp.StateDisabled:
		return ErrStyle.Render("disabled")
	case mcp.StatePending:
		return WarnStyle.Render("pending")
	default:
		return DimStyle.Render("—")
	}
}

// runMCPUI drives the interactive MCP screen in a loop: render the snapshot,
// perform the chosen action (writing files, or running the add form, outside the
// event loop), then re-open with a fresh snapshot until the user backs out.
func runMCPUI(cmd *cobra.Command) error {
	home, repo, err := mcpHomeRepo(cmd.Context())
	if err != nil {
		return err
	}
	note := ""
	for {
		entries, err := mcp.List(home, repo)
		if err != nil {
			return err
		}
		res, err := tea.NewProgram(tui.NewMCP(entries, repo != "", note)).Run()
		if err != nil {
			return err
		}
		final, ok := res.(tui.MCPModel)
		if !ok {
			return nil
		}
		note = ""
		switch final.Action.Kind {
		case "":
			return nil
		case "add":
			n, err := mcpAddInteractive(cmd, home, repo)
			if err != nil {
				if errors.Is(err, huh.ErrUserAborted) {
					continue
				}
				return err
			}
			note = "added " + n
		case "remove":
			removed, err := mcp.Remove(final.Action.Scope, home, repo, final.Action.Name)
			if err != nil {
				return err
			}
			if removed {
				note = "removed " + final.Action.Name
			}
		case "enable":
			if err := mcp.SetEnabled(home, repo, final.Action.Name, true); err != nil {
				return err
			}
			note = "enabled " + final.Action.Name
		case "disable":
			if err := mcp.SetEnabled(home, repo, final.Action.Name, false); err != nil {
				return err
			}
			note = "disabled " + final.Action.Name
		}
	}
}

// mcpAddInteractive runs the huh add form and writes the new server, returning
// its name. ErrUserAborted means the user escaped the form.
func mcpAddInteractive(cmd *cobra.Command, home, repo string) (string, error) {
	scopeVal := string(settings.User)
	transport := mcp.TransportStdio
	var name, target, argsLine, envText, headerText string

	scopeOpts := []huh.Option[string]{huh.NewOption("user — ~/.claude.json (all projects)", "user")}
	if repo != "" {
		scopeOpts = append(scopeOpts,
			huh.NewOption("project — .mcp.json (committed, shared)", "project"),
			huh.NewOption("local — this checkout only", "local"))
	}

	isStdio := func() bool { return transport == mcp.TransportStdio }
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().Title("Scope").Options(scopeOpts...).Value(&scopeVal),
			huh.NewSelect[string]().Title("Transport").Options(
				huh.NewOption("stdio (local command)", mcp.TransportStdio),
				huh.NewOption("http", mcp.TransportHTTP),
				huh.NewOption("sse", mcp.TransportSSE),
			).Value(&transport),
			huh.NewInput().Title("Server name").Value(&name).Validate(nonEmpty("name")),
		),
		huh.NewGroup( // stdio
			huh.NewInput().Title("Command").Placeholder("npx").Value(&target).Validate(nonEmpty("command")),
			huh.NewInput().Title("Args (space-separated)").Placeholder("-y @scope/pkg").Value(&argsLine),
			huh.NewText().Title("Env (KEY=VALUE per line)").Value(&envText),
		).WithHideFunc(func() bool { return !isStdio() }),
		huh.NewGroup( // http / sse
			huh.NewInput().Title("URL").Placeholder("https://example.com/mcp").Value(&target).Validate(nonEmpty("url")),
			huh.NewText().Title("Headers (KEY=VALUE per line)").Value(&headerText),
		).WithHideFunc(isStdio),
	).WithTheme(brand.Theme(brand.AccentClaude)).WithKeyMap(huhEscKeyMap())

	if err := form.Run(); err != nil {
		return "", err
	}

	scope, err := settings.Parse(scopeVal)
	if err != nil {
		return "", err
	}
	rest := append([]string{target}, strings.Fields(argsLine)...)
	srv, err := buildServer(transport, rest, splitLines(envText), splitLines(headerText))
	if err != nil {
		return "", err
	}
	if err := mcp.Add(scope, home, repo, name, srv); err != nil {
		return "", err
	}
	return name, nil
}

// nonEmpty is a huh validator rejecting blank input for the named field.
func nonEmpty(field string) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("%s is required", field)
		}
		return nil
	}
}

// splitLines returns the non-blank, trimmed lines of s (for env/header blocks).
func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}
