package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/cfgfind"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/confkit"
	"github.com/spf13/cobra"
)

// changesetSchemaURL is stamped onto a freshly written .changeset/config.json.
const changesetSchemaURL = "https://rigsmith.dev/schemas/changeset-config.json"

// configWriter is the shared JSONC writer pinned to the changeset schema.
var configWriter = confkit.Writer{SchemaURL: changesetSchemaURL}

// settableKey is a top-level scalar config key `config set` understands, with an
// optional set of allowed values (empty = any string).
type settableKey struct {
	name    string
	allowed []string
	help    string
}

// settableKeys are the scalar config knobs editable via `config set`. Richer
// fields (ignore, fixed, linked, changelogGroups, ecosystem blocks) stay in
// `config edit` / hand-editing — they don't reduce to a single value.
var settableKeys = []settableKey{
	{"baseBranch", nil, "branch changes are compared against (e.g. main)"},
	{"access", []string{"public", "restricted"}, "npm publish access"},
	{"updateInternalDependencies", []string{"patch", "minor"}, "how far dependents are bumped"},
	{"versionStrategy", []string{"lockstep", "independent"}, "shared-version bump strategy"},
}

func lookupKey(name string) (settableKey, bool) {
	for _, k := range settableKeys {
		if k.name == name {
			return k, true
		}
	}
	return settableKey{}, false
}

// NewConfigCmd builds the uniform `config` command group: get / set / path /
// edit / show over .changeset/config.json.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View or change .changeset/config.json",
	}
	cmd.AddCommand(
		newConfigShowCmd(),
		newConfigGetCmd(),
		newConfigSetCmd(),
		newConfigPathCmd(),
		newConfigEditCmd(),
	)
	return cmd
}

// configFile resolves the config file the `config` command should read/write.
// It targets the repo's single existing config file across the allowed
// locations; when the config lives in a .rig.json key it returns an error
// (edit it there), and when none exists yet it defaults to the canonical
// .changeset/config.json so `config set` scaffolds the conventional spot.
func configFile() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root, err := FindRoot(cwd)
	if err != nil {
		return "", err
	}
	changesetDir := filepath.Join(root, ".changeset")
	src, err := cfgfind.Find(config.Spec(changesetDir))
	if err != nil {
		return "", err // ambiguous — names every candidate
	}
	switch {
	case src == nil:
		return filepath.Join(changesetDir, "config.json"), nil // canonical default
	case src.Path == "":
		return "", fmt.Errorf("changeset config lives in %s — edit it there", src.Origin)
	default:
		return src.Path, nil
	}
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the whole config file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configFile()
			if err != nil {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "no config yet — run `changerig init`")
				return nil
			}
			fmt.Fprint(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get [key]",
		Short: "Print one setting (or all scalar settings when no key is given)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := Open()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				for _, k := range settableKeys {
					fmt.Fprintf(out, "%s = %s\n", k.name, configValue(ws, k.name))
				}
				return nil
			}
			if _, ok := lookupKey(args[0]); !ok {
				return unknownKeyErr(args[0])
			}
			fmt.Fprintln(out, configValue(ws, args[0]))
			return nil
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set one scalar setting",
		Long:  "Set a scalar key in .changeset/config.json (comments preserved). Keys:\n" + settableKeysHelp(),
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			spec, ok := lookupKey(key)
			if !ok {
				return unknownKeyErr(key)
			}
			if len(spec.allowed) > 0 && !contains(spec.allowed, value) {
				return fmt.Errorf("invalid value %q for %s (allowed: %s)", value, key, strings.Join(spec.allowed, ", "))
			}
			path, err := configFile()
			if err != nil {
				return err
			}
			if !configWriter.SetString(path, []string{key}, value) {
				return fmt.Errorf("could not write %s (refusing to clobber a file that can't be edited in place)", path)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ %s = %s\n", key, value)
			return nil
		},
	}
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the path to the config file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configFile()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
}

func newConfigEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open the config file in $VISUAL/$EDITOR",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := configFile()
			if err != nil {
				return err
			}
			return openInEditor(cmd, path)
		},
	}
}

// configValue renders the current (effective) value of a scalar key.
func configValue(ws *Workspace, key string) string {
	c := ws.Config
	switch key {
	case "baseBranch":
		return c.BaseBranch
	case "access":
		return c.Access
	case "updateInternalDependencies":
		return string(c.UpdateInternalDependencies)
	case "versionStrategy":
		if c.VersionStrategy == "" {
			return string(c.VersionStrategy) // empty means lockstep; show as set
		}
		return string(c.VersionStrategy)
	default:
		return ""
	}
}

func settableKeysHelp() string {
	var b strings.Builder
	for _, k := range settableKeys {
		allowed := "any"
		if len(k.allowed) > 0 {
			allowed = strings.Join(k.allowed, "|")
		}
		fmt.Fprintf(&b, "  %-28s %s [%s]\n", k.name, k.help, allowed)
	}
	return b.String()
}

func unknownKeyErr(key string) error {
	names := make([]string, len(settableKeys))
	for i, k := range settableKeys {
		names[i] = k.name
	}
	sort.Strings(names)
	return fmt.Errorf("unknown config key %q (settable: %s) — use `config edit` for other fields", key, strings.Join(names, ", "))
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
