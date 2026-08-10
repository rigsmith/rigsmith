package cli

import (
	"fmt"
	"os"

	"github.com/rigsmith/rigsmith/core/envstack"
)

// noEnv backs the persistent --no-env flag: when set, the .env/.env.local file
// layer is dropped for the run (the ambient environment still flows through).
var noEnv bool

// loadReleaseEnv builds the layered release environment used by `release` (for
// ${env.NAME} interpolation, spawned commands, and forge releases) and by
// `init`'s token preflight: .env/.env.local from root, layered under the
// ambient process environment (file < ambient — a real export always wins).
// When noEnv is set the file layer is skipped, leaving just the ambient env. A
// read error on a present .env or .env.local is fatal; missing files are not.
func loadReleaseEnv(root string, noEnv bool) (map[string]string, error) {
	var fileEnv map[string]string
	if !noEnv {
		var err error
		fileEnv, err = envstack.Load(root)
		if err != nil {
			return nil, fmt.Errorf("loading .env/.env.local: %w", err)
		}
	}
	return envstack.Merge(fileEnv, envstack.Ambient(), nil, nil), nil
}

// applyReleaseEnv loads the layered release environment (see loadReleaseEnv) and
// exports it into this process's own environment for the rest of the run,
// returning the same map.
//
// `publish` hands the actual credential work to code that reads the *process*
// environment, not a map we could thread through: the dotnet adapter's
// NUGET_API_KEY fallback, core/auth's `env:NAME` refs and its OIDC-context
// probe, and the package managers themselves (dotnet/npm/cargo), which the
// adapters spawn with an inherited environment. `shiprig release` gets this for
// free — it runs `shiprig publish` as a subprocess seeded with the layered env —
// so a token declared only in .env works under `release` and used to vanish
// under a direct `publish`. Exporting the layer here closes that gap.
//
// The ambient environment still wins (loadReleaseEnv's file < ambient
// precedence), so this only ever fills in keys the shell did not already set,
// and --no-env drops the file layer, making it a no-op.
func applyReleaseEnv(root string, noEnv bool) (map[string]string, error) {
	env, err := loadReleaseEnv(root, noEnv)
	if err != nil {
		return nil, err
	}
	for k, v := range env {
		if cur, ok := os.LookupEnv(k); ok && cur == v {
			continue // already exported with this value (it came from the ambient layer)
		}
		if err := os.Setenv(k, v); err != nil {
			return nil, fmt.Errorf("exporting %s from .env: %w", k, err)
		}
	}
	return env, nil
}
