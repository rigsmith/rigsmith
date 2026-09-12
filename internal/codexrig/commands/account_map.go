package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/internal/agentrig/dirmap"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/spf13/cobra"
)

// dirMap opens this machine's directory bindings. Per-machine and never synced:
// it maps THIS computer's paths, and another machine's would be meaningless.
func dirMap() (*dirmap.Store, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	return dirmap.New(filepath.Join(dir, "dir-map.json")), nil
}

func newAccountMapCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "map [<id|email|alias>] [dir]",
		Short: "Bind a directory to an account",
		Long: "After this, a bare `codexrig account run` inside that directory — or anywhere\n" +
			"under it — starts Codex as that login, without naming it.\n\n" +
			"Nearest mapped ancestor wins, so a binding on a repository covers every\n" +
			"worktree under it, and a binding on one worktree overrides it. With no\n" +
			"arguments, lists what is bound.\n\n" +
			"Per-machine, and never synced: it maps this computer's paths.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			store, err := dirMap()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				entries, lerr := store.List()
				if lerr != nil {
					return lerr
				}
				if len(entries) == 0 {
					fmt.Fprintln(out, DimStyle.Render("no directory bindings — `codexrig account map <account> [dir]`"))
					return nil
				}
				fmt.Fprintln(out, HeaderStyle.Render("Directory bindings"))
				for _, e := range entries {
					fmt.Fprintf(out, "  %s\n", tildeHome(e.Dir))
					fmt.Fprintf(out, "    %s\n", DimStyle.Render("account "+e.Account))
				}
				return nil
			}

			s, aerr := openStore()
			if aerr != nil {
				return aerr
			}
			a, rerr := s.Resolve(args[0])
			if rerr != nil {
				return rerr
			}
			dir, derr := mapTarget(args)
			if derr != nil {
				return derr
			}
			if _, err := store.Set(dir, func(e *dirmap.Entry) { e.Account = a.ID }); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s %s → %s\n", OkStyle.Render("✓ mapped"), tildeHome(dir), a.Title())
			fmt.Fprintf(out, "  %s\n", DimStyle.Render("`codexrig account run` there now picks it"))
			return nil
		},
	}
}

func newAccountUnmapCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unmap [dir]",
		Short: "Remove a directory's account binding",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := dirMap()
			if err != nil {
				return err
			}
			dir, derr := mapTarget(append([]string{""}, args...))
			if derr != nil {
				return derr
			}
			// Only the account binding is cleared. Another rig may have bound
			// the same directory in the same file, and removing the whole entry
			// would take its binding with it.
			if _, err := store.Set(dir, func(e *dirmap.Entry) { e.Account = "" }); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", OkStyle.Render("✓ unmapped"), tildeHome(dir))
			return nil
		},
	}
}

// mapTarget resolves the directory argument, defaulting to the working
// directory — which is what somebody standing in a project means.
func mapTarget(args []string) (string, error) {
	if len(args) >= 2 && args[1] != "" {
		return filepath.Abs(args[1])
	}
	return os.Getwd()
}

// mappedAccount reports the account bound to a directory. A missing mapping is
// ("", nil); anything else is an error.
//
// The distinction is load-bearing. Collapsing both into "" made an unreadable
// or malformed mapping look like "no binding here", and the caller then falls
// back to "there happens to be only one enabled account" — which starts Codex
// under a login the user explicitly bound this directory away from.
func mappedAccount(dir string) (string, error) {
	store, err := dirMap()
	if err != nil {
		return "", err
	}
	e, err := store.Lookup(dir)
	if errors.Is(err, dirmap.ErrNoMapping) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return e.Account, nil
}
