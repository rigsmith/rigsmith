package cli

import (
	"fmt"

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
// read error on a present .env is fatal; a missing file is not an error.
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
