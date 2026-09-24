package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/cfgfind"
	"github.com/rigsmith/rigsmith/core/climenu"
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
		// Bare `config` on a TTY opens the subcommand menu; with a verb or off a
		// TTY the subcommands stand (and `config -h` still prints help).
		RunE: func(cmd *cobra.Command, args []string) error {
			if Interactive() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
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

// resolveConfigSource resolves the repo's single changeset config across its
// allowed locations (a file or an inline .rig.json key). changesetDir is the
// canonical .changeset path; src is nil when no config exists yet.
func resolveConfigSource() (src *cfgfind.Source, changesetDir string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	root, err := FindRoot(cwd)
	if err != nil {
		return nil, "", err
	}
	changesetDir = filepath.Join(root, ".changeset")
	src, err = cfgfind.Find(config.Spec(changesetDir))
	return src, changesetDir, err
}

// configFile resolves the config FILE for read/write operations. It targets the
// single existing config file; when the config lives in a .rig.json key it
// returns an error (set/edit must point at a file); with none it defaults to the
// canonical .changeset/config.json so `config set` scaffolds the conventional
// spot.
func configFile() (string, error) {
	src, changesetDir, err := resolveConfigSource()
	if err != nil {
		return "", err
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
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the whole config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			src, _, err := resolveConfigSource()
			if err != nil {
				return err
			}
			if asJSON {
				return writeResolvedConfig(cmd.OutOrStdout(), src)
			}
			if src == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "no config yet — run `changerig init`")
				return nil
			}
			out := strings.TrimRight(string(src.Data), "\n")
			fmt.Fprintln(cmd.OutOrStdout(), out) // works for a file or an inline .rig.json key
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the config as parsed, defaults applied, as plain JSON (for scripts)")
	return cmd
}

// writeResolvedConfig prints the config shiprig runs with as plain JSON: the
// resolved source parsed (JSONC stripped), defaults applied, ecosystem blocks
// kept, and `versioning.source` always present with its effective value, so
// a script needn't know where the config lives, how to read JSONC, or the
// defaults. No config at all prints the defaults.
func writeResolvedConfig(w io.Writer, src *cfgfind.Source) error {
	cfg := config.Default()
	if src != nil {
		parsed, err := config.Parse(src.Data)
		if err != nil {
			return err
		}
		cfg = parsed
	}
	typed, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	out := map[string]any{}
	if err := decodeExact(typed, &out); err != nil {
		return err
	}
	for name, block := range cfg.Ecosystems {
		var v any
		if err := decodeExact(block, &v); err != nil {
			return fmt.Errorf("config: %s: %w", name, err)
		}
		out[name] = v
	}
	versioning, _ := out["versioning"].(map[string]any)
	if versioning == nil {
		versioning = map[string]any{}
	}
	versioning["source"] = string(cfg.CommitSource())
	out["versioning"] = versioning
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
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
		Short: "Print where the config is resolved from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			src, changesetDir, err := resolveConfigSource()
			if err != nil {
				return err
			}
			if src == nil {
				// None yet — name the canonical spot `set`/`init` would create.
				fmt.Fprintln(cmd.OutOrStdout(), filepath.Join(changesetDir, "config.json"))
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), src.Origin) // a file path, or `.rig.json ("changerig" key)`
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

// decodeExact decodes JSON keeping numbers as written (json.Number), so a
// large integer doesn't lose digits through float64 on its way back out.
func decodeExact(data []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(dst)
}
