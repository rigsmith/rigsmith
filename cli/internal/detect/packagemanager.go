package detect

import (
	"os"
	"path/filepath"

	"github.com/rigsmith/core/plugin"
)

// NodePM is a detected Node package manager.
type NodePM string

const (
	NPM  NodePM = "npm"
	PNPM NodePM = "pnpm"
	Yarn NodePM = "yarn"
	Bun  NodePM = "bun"
)

// DetectNodePM picks the package manager for a Node repo from its lockfile,
// defaulting to npm. (A lightweight take on @antfu/ni — enough for the common
// case; the exact yarn-classic-vs-berry split is not yet modeled.)
func DetectNodePM(root string) NodePM {
	for _, c := range []struct {
		lock string
		pm   NodePM
	}{
		{"bun.lockb", Bun},
		{"bun.lock", Bun},
		{"pnpm-lock.yaml", PNPM},
		{"yarn.lock", Yarn},
		{"package-lock.json", NPM},
	} {
		if _, err := os.Stat(filepath.Join(root, c.lock)); err == nil {
			return c.pm
		}
	}
	return NPM
}

// nodeScript maps a dev-loop verb to its package.json script name.
var nodeScript = map[string]string{
	plugin.VerbBuild:     "build",
	plugin.VerbTest:      "test",
	plugin.VerbRun:       "dev",
	plugin.VerbFormat:    "format",
	plugin.VerbLint:      "lint",
	plugin.VerbTypecheck: "typecheck",
	plugin.VerbCoverage:  "coverage",
}

// nodeCommand returns the argv for a verb under the given package manager,
// applying ni-style per-manager command differences. ok=false means this verb
// isn't a recognized Node command (caller falls back to the static map).
func nodeCommand(pm NodePM, verb string) (argv []string, ok bool) {
	bin := string(pm)

	if script, isScript := nodeScript[verb]; isScript {
		return []string{bin, "run", script}, true
	}

	switch verb {
	case plugin.VerbInstall:
		return []string{bin, "install"}, true
	case plugin.VerbAdd:
		if pm == NPM {
			return []string{bin, "install"}, true
		}
		return []string{bin, "add"}, true
	case plugin.VerbUninstall:
		if pm == NPM {
			return []string{bin, "uninstall"}, true
		}
		return []string{bin, "remove"}, true
	case plugin.VerbOutdated:
		return []string{bin, "outdated"}, true
	case plugin.VerbUpgrade:
		if pm == Yarn {
			return []string{bin, "upgrade"}, true
		}
		return []string{bin, "update"}, true
	case plugin.VerbCI:
		switch pm {
		case NPM:
			return []string{bin, "ci"}, true
		case Yarn:
			return []string{bin, "install", "--immutable"}, true
		default: // pnpm, bun
			return []string{bin, "install", "--frozen-lockfile"}, true
		}
	case plugin.VerbGlobal:
		switch pm {
		case Yarn:
			return []string{bin, "global", "add"}, true
		case Bun:
			return []string{bin, "add", "--global"}, true
		case PNPM:
			return []string{bin, "add", "--global"}, true
		default: // npm
			return []string{bin, "install", "--global"}, true
		}
	case plugin.VerbDlx:
		switch pm {
		case NPM:
			return []string{"npx"}, true
		case Bun:
			return []string{"bun", "x"}, true
		default: // pnpm, yarn
			return []string{bin, "dlx"}, true
		}
	}
	return nil, false
}
