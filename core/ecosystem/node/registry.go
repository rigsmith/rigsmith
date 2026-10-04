package node

import (
	"encoding/json"
	"fmt"
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
// A chosen registry that refers to an environment variable that is not set (`@acme:registry=${ACME_REGISTRY}`) is an
// error, as it is in npm: falling back to the default would send a private package to npmjs.com. Unset references
// elsewhere in an .npmrc — an `_authToken=${NPM_TOKEN}` beside the routing, say — are not this function's business.
//
// Credentials stay npm's: the caller's ~/.npmrc carries the token for whichever host this returns.
func npmRegistry(repoRoot string, pkg plugin.Package, source string) (string, error) {
	if strings.HasPrefix(source, "http") {
		return source, nil
	}
	repoRoot = filepath.Clean(repoRoot) // a trailing separator must not let the walk below pass the root
	dir := filepath.Join(repoRoot, pkg.Dir)
	if reg := publishConfigRegistry(dir); reg != "" {
		return reg, nil
	}

	type npmrc struct {
		path string
		kv   map[string]string
	}
	var files []npmrc // nearest first
	for d := dir; ; d = filepath.Dir(d) {
		path := filepath.Join(d, ".npmrc")
		if kv := readNpmrc(path); kv != nil {
			files = append(files, npmrc{path, kv})
		}
		if !strings.HasPrefix(d, repoRoot) || d == repoRoot || d == filepath.Dir(d) {
			break
		}
	}
	var keys []string
	if scope := packageScope(pkg.Name); scope != "" {
		keys = append(keys, scope+":registry")
	}
	keys = append(keys, "registry")
	for _, key := range keys {
		for _, f := range files {
			if raw, ok := f.kv[key]; ok && raw != "" {
				reg, err := expandNpmrcValue(raw)
				if err != nil {
					return "", fmt.Errorf("%s in %s: %w", key, f.path, err)
				}
				return reg, nil
			}
		}
	}
	return "", nil
}

// npmRegistryArgs are the flags that send npm to registry for the named package, or none for npm's default.
//
// `--registry` alone is not enough for a scoped package: npm picks a scoped package's registry from `@scope:registry`
// in ANY config layer before the plain `registry` key, so a user ~/.npmrc routing the scope elsewhere would win over
// `--registry` and npm would talk to that other registry while the auth was set up for this one. The command line
// outranks every config file, so the scoped key goes there too.
func npmRegistryArgs(name, registry string) []string {
	if registry == "" {
		return nil
	}
	args := []string{"--registry", registry}
	if scope := packageScope(name); scope != "" {
		args = append(args, "--"+scope+":registry="+registry)
	}
	return args
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

// readNpmrc parses an .npmrc into key → raw value, or nil when there is no such file. Comments (`#`, `;`) and blank
// lines are skipped and a quoted value is unquoted. `${VAR}` references are left as they are: only the value a caller
// actually uses is expanded (expandNpmrcValue), so an unset reference in an unrelated key is never an error.
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
		kv[strings.TrimSpace(key)] = value
	}
	return kv
}

var npmrcEnvRef = regexp.MustCompile(`\$\{([^}]+)\}`)

// expandNpmrcValue expands `${VAR}` from the environment, as npm does, and fails — as npm does — when a referenced
// variable is not set, or the result is empty.
func expandNpmrcValue(raw string) (string, error) {
	var missing []string
	value := npmrcEnvRef.ReplaceAllStringFunc(raw, func(ref string) string {
		name := ref[2 : len(ref)-1]
		v, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("refers to %s, which is not set", strings.Join(missing, ", "))
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%q expands to nothing", raw)
	}
	return value, nil
}
