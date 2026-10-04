package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// npmRegistry is the registry a package is looked up on and published to, as a URL, or "" for npm's own default.
//
// npm reads a project .npmrc only from the directory it runs in — here the package's — and never from a workspace
// root above it. A pnpm or Yarn workspace usually routes its scope to a private registry in the ROOT .npmrc, which
// pnpm honours; npm, run in the package directory, misses it and asks npmjs.com instead. So `npm view` reported a
// version already on the private registry as unpublished, and `npm publish` would have sent the package to npmjs.com.
// This resolves the registry the way the workspace means it, in order:
//
//  1. the configured package source, when it is a URL (unchanged behaviour);
//  2. the package's own publishConfig.registry — where npm publishes it, so also where it is found;
//  3. the nearest .npmrc from the package directory up to the repository root: its `@scope:registry` for the
//     package's scope, else its plain `registry`. A scoped entry in any of them wins over a plain one, as in npm.
//
// Credentials stay npm's: the caller's ~/.npmrc carries the token for whichever host this returns.
func npmRegistry(repoRoot string, pkg plugin.Package, source string) string {
	if strings.HasPrefix(source, "http") {
		return source
	}
	repoRoot = filepath.Clean(repoRoot) // a trailing separator must not let the walk below pass the root
	dir := filepath.Join(repoRoot, pkg.Dir)
	if reg := publishConfigRegistry(dir); reg != "" {
		return reg
	}

	var files []map[string]string // nearest first
	for d := dir; ; d = filepath.Dir(d) {
		if kv := readNpmrc(filepath.Join(d, ".npmrc")); kv != nil {
			files = append(files, kv)
		}
		if !strings.HasPrefix(d, repoRoot) || d == repoRoot || d == filepath.Dir(d) {
			break
		}
	}
	if scope := packageScope(pkg.Name); scope != "" {
		for _, kv := range files {
			if reg := kv[scope+":registry"]; reg != "" {
				return reg
			}
		}
	}
	for _, kv := range files {
		if reg := kv["registry"]; reg != "" {
			return reg
		}
	}
	return ""
}

// publishConfigRegistry reads publishConfig.registry from the package.json in dir, or "".
func publishConfigRegistry(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		PublishConfig struct {
			Registry string `json:"registry"`
		} `json:"publishConfig"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return ""
	}
	return strings.TrimSpace(manifest.PublishConfig.Registry)
}

// packageScope is "@acme" for "@acme/lib", and "" for an unscoped name.
func packageScope(name string) string {
	if !strings.HasPrefix(name, "@") {
		return ""
	}
	scope, _, ok := strings.Cut(name, "/")
	if !ok {
		return ""
	}
	return scope
}

var npmrcEnvRef = regexp.MustCompile(`\$\{([^}]+)\}`)

// readNpmrc parses an .npmrc into key → value, or nil when there is no such file. Comments (`#`, `;`) and blank
// lines are skipped, a quoted value is unquoted, and `${VAR}` expands from the environment, as npm does.
func readNpmrc(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	kv := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		value = npmrcEnvRef.ReplaceAllStringFunc(value, func(ref string) string {
			return os.Getenv(ref[2 : len(ref)-1])
		})
		kv[strings.TrimSpace(key)] = value
	}
	return kv
}
