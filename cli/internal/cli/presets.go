package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// presetFlag binds a `.rig.json` env-preset name to the bool flag that selects
// it. When the flag is set, the preset's env bundle is applied to the spawned
// process (see activePresetEnv + commandEnv).
type presetFlag struct {
	name string
	on   *bool
}

// presetFlagReserved are names a preset flag must not shadow: the root
// persistent flags and the dev-verb flags (cobra panics on a duplicate, and the
// reserved meaning must win).
var presetFlagReserved = map[string]bool{
	"dry-run": true, "quiet": true, "no-env": true, "root": true,
	"help": true, "all": true, "filter": true, "watch": true, "pick": true,
}

// registerPresetFlags adds one boolean flag per `.rig.json` env preset to cmd,
// so `rig <verb> --<preset>` applies that preset's env bundle. A preset whose
// name collides with an existing or reserved flag is skipped. Returns the
// registered presets for the RunE to resolve at run time.
func registerPresetFlags(cmd *cobra.Command) []presetFlag {
	cwd, _ := os.Getwd()
	cfg, err := config.LoadMerged(detect.Root(cwd))
	if err != nil || cfg.Test == nil || len(cfg.Test.EnvPresets) == 0 {
		return nil
	}
	names := make([]string, 0, len(cfg.Test.EnvPresets))
	for name := range cfg.Test.EnvPresets {
		names = append(names, name)
	}
	sort.Strings(names)

	var presets []presetFlag
	for _, name := range names {
		if presetFlagReserved[name] || cmd.Flags().Lookup(name) != nil {
			continue
		}
		on := new(bool)
		cmd.Flags().BoolVar(on, name, false, fmt.Sprintf("apply the %q env preset", name))
		presets = append(presets, presetFlag{name: name, on: on})
	}
	return presets
}

// activePresetEnv collects the env vars of the presets whose flag is set,
// reloading config at root so an explicit --root is honored. Presets are merged
// in sorted order, so a later preset wins on a key conflict (deterministic).
// Returns nil when nothing is active.
func activePresetEnv(root string, presets []presetFlag) map[string]string {
	var active []string
	for _, p := range presets {
		if p.on != nil && *p.on {
			active = append(active, p.name)
		}
	}
	if len(active) == 0 {
		return nil
	}
	cfg, err := config.LoadMerged(root)
	if err != nil || cfg.Test == nil {
		return nil
	}
	out := map[string]string{}
	for _, name := range active {
		for k, v := range cfg.Test.EnvPresets[name] {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
