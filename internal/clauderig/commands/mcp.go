package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
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

// mcpHomeRepo resolves the home dir and the project directory the mcp commands
// operate on. Like `claude mcp`, the project dir is the git repo root when inside
// one, else the current working directory — so the project/local scopes work
// everywhere, not only inside a repo.
func mcpHomeRepo(ctx context.Context) (home, dir string, err error) {
	home, err = os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	dir = repoRootBestEffort(ctx)
	if dir == "" {
		if cwd, werr := os.Getwd(); werr == nil {
			dir = cwd
		}
	}
	return home, dir, nil
}

// scopeFlag adds the shared --scope flag and returns a resolver that parses it.
// The default mirrors `claude mcp` (local — this project, just you).
func scopeFlag(cmd *cobra.Command, def settings.Scope) func() (settings.Scope, error) {
	var raw string
	cmd.Flags().StringVarP(&raw, "scope", "s", string(def),
		"local (this project, just you) | user (all your projects) | project (shared via .mcp.json)")
	return func() (settings.Scope, error) { return settings.Parse(raw) }
}

func newMCPListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List configured MCP servers across scopes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, dir, err := mcpHomeRepo(cmd.Context())
			if err != nil {
				return err
			}
			entries, err := mcp.List(home, dir)
			if err != nil {
				return err
			}
			return printServerList(cmd, entries)
		},
	}
	return cmd
}

func newMCPGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Show an MCP server's configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, dir, err := mcpHomeRepo(cmd.Context())
			if err != nil {
				return err
			}
			// Like `claude mcp get`, search every scope rather than make the user
			// name one. A server can exist at more than one scope; show each.
			entries, err := mcp.List(home, dir)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			found := false
			for _, e := range entries {
				if e.Name != args[0] {
					continue
				}
				found = true
				printServerDetail(out, e)
			}
			if !found {
				return fmt.Errorf("no MCP server named %q", args[0])
			}
			return nil
		},
	}
}

// printServerDetail renders one server's full configuration.
func printServerDetail(out io.Writer, e mcp.Entry) {
	fmt.Fprintf(out, "%s %s\n", HeaderStyle.Render(e.Name), DimStyle.Render("("+string(e.Scope)+")"))
	fmt.Fprintf(out, "  transport  %s\n", e.Server.Transport())
	if e.Server.Command != "" {
		fmt.Fprintf(out, "  command    %s\n", e.Server.Command)
	}
	if len(e.Server.Args) > 0 {
		fmt.Fprintf(out, "  args       %s\n", strings.Join(e.Server.Args, " "))
	}
	if e.Server.URL != "" {
		fmt.Fprintf(out, "  url        %s\n", e.Server.URL)
	}
	for k, v := range e.Server.Env {
		fmt.Fprintf(out, "  env        %s=%s\n", k, v)
	}
	for k, v := range e.Server.Headers {
		fmt.Fprintf(out, "  header     %s=%s\n", k, v)
	}
	if e.State != mcp.StateNA {
		fmt.Fprintf(out, "  state      %s\n", stateText(e.State))
	}
}

func newMCPAddCmd() *cobra.Command {
	var resolve func() (settings.Scope, error)
	var transport string
	var env, headers []string
	cmd := &cobra.Command{
		Use:   "add <name> <command|url> [args...]",
		Short: "Add (or replace) an MCP server",
		Long: "Add an MCP server. claudeRig's own flags (--scope, --transport, --env,\n" +
			"--header) come before <name>; everything after is the server's command/url\n" +
			"and its args, so a server's own flags (e.g. `npx -y pkg`) pass through.\n\n" +
			"stdio (the default) — give the command and its args:\n" +
			"  clauderig mcp add --env KEY=val ctx7 npx -y @upstash/context7-mcp\n" +
			"http/sse — give the URL:\n" +
			"  clauderig mcp add -t http -H Authorization=Bearer... docs https://example.com/mcp",
		Args: cobra.MinimumNArgs(2), // <name> plus a command (stdio) or URL (http/sse)
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := resolve()
			if err != nil {
				return err
			}
			home, dir, err := mcpHomeRepo(cmd.Context())
			if err != nil {
				return err
			}
			name := args[0]
			srv, err := buildServer(transport, args[1:], env, headers)
			if err != nil {
				return err
			}
			if _, exists, _ := mcp.Get(scope, home, dir, name); exists {
				fmt.Fprintf(cmd.OutOrStdout(), "%s replacing existing %s-scope server %q\n", WarnStyle.Render("!"), scope, name)
			}
			if err := mcp.Add(scope, home, dir, name, srv); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s added %s %s\n", OkStyle.Render("✓"), name, DimStyle.Render("("+string(scope)+")"))
			return nil
		},
	}
	resolve = scopeFlag(cmd, settings.Local)
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
			home, dir, err := mcpHomeRepo(cmd.Context())
			if err != nil {
				return err
			}
			removed, err := mcp.Remove(scope, home, dir, args[0])
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
	resolve = scopeFlag(cmd, settings.Local)
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
			home, dir, err := mcpHomeRepo(cmd.Context())
			if err != nil {
				return err
			}
			if err := mcp.SetEnabled(home, dir, args[0], enabled); err != nil {
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
		if len(rest) > 1 {
			// A header passed as a positional (forgetting -H) would otherwise be
			// silently dropped — reject it instead of hiding the mistake.
			return srv, fmt.Errorf("%s server takes a single URL; use -H for headers (unexpected: %s)",
				transport, strings.Join(rest[1:], " "))
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
func printServerList(cmd *cobra.Command, entries []mcp.Entry) error {
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
	home, dir, err := mcpHomeRepo(cmd.Context())
	if err != nil {
		return err
	}
	note := ""
	for {
		entries, err := mcp.List(home, dir)
		if err != nil {
			return err
		}
		res, err := tea.NewProgram(tui.NewMCP(entries, note)).Run()
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
			n, err := mcpAddInteractive(cmd, home, dir)
			if err != nil {
				if errors.Is(err, huh.ErrUserAborted) {
					continue
				}
				return err
			}
			note = "added " + n
		case "remove":
			removed, err := mcp.Remove(final.Action.Scope, home, dir, final.Action.Name)
			if err != nil {
				return err
			}
			if removed {
				note = "removed " + final.Action.Name
			}
		case "enable":
			if err := mcp.SetEnabled(home, dir, final.Action.Name, true); err != nil {
				return err
			}
			note = "enabled " + final.Action.Name
		case "disable":
			if err := mcp.SetEnabled(home, dir, final.Action.Name, false); err != nil {
				return err
			}
			note = "disabled " + final.Action.Name
		}
	}
}

// mcpAddInteractive runs the huh add form and writes the new server, returning
// its name. ErrUserAborted means the user escaped the form. Scope defaults to
// local, matching `claude mcp add`.
func mcpAddInteractive(cmd *cobra.Command, home, dir string) (string, error) {
	scopeVal := string(settings.Local)
	transport := mcp.TransportHTTP
	var name, target, argsLine, envText, headerText string

	scopeOpts := []huh.Option[string]{
		huh.NewOption("local — this project, just you (default)", "local"),
		huh.NewOption("project — .mcp.json (committed, shared)", "project"),
		huh.NewOption("user — ~/.claude.json (all your projects)", "user"),
	}

	isStdio := func() bool { return transport == mcp.TransportStdio }
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().Title("Scope").Options(scopeOpts...).Value(&scopeVal),
			huh.NewSelect[string]().Title("Transport").Options(
				huh.NewOption("http (default)", mcp.TransportHTTP),
				huh.NewOption("stdio (local command)", mcp.TransportStdio),
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
	if err := mcp.Add(scope, home, dir, name, srv); err != nil {
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
