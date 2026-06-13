package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// viteConfigNames are the Vite config file variants that signal a Vite project.
var viteConfigNames = []string{
	"vite.config.ts", "vite.config.js", "vite.config.mts",
	"vite.config.mjs", "vite.config.cts", "vite.config.cjs",
}

// viteDefaultPort is Vite's built-in dev-server port.
const viteDefaultPort = 5173

// NodeUsesVite reports whether the node project at root uses Vite — a vite
// config file at the root, or `vite` in its (dev)dependencies.
func NodeUsesVite(root string) bool {
	for _, n := range viteConfigNames {
		if _, err := os.Stat(filepath.Join(root, n)); err == nil {
			return true
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return false
	}
	var pj struct {
		Deps map[string]string `json:"dependencies"`
		Dev  map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(data, &pj) != nil {
		return false
	}
	if _, ok := pj.Deps["vite"]; ok {
		return true
	}
	_, ok := pj.Dev["vite"]
	return ok
}

var (
	// devPortFlagRe matches an explicit --port in the package.json dev script
	// (`vite --port 3000` / `--port=3000`).
	devPortFlagRe = regexp.MustCompile(`--port[ =]\s*(\d{2,5})`)
	// configPortRe matches a `port: 3000` in a vite config (best-effort — the
	// first one wins; the user can always `rig kill --port N` to be explicit).
	configPortRe = regexp.MustCompile(`(?m)port\s*:\s*(\d{2,5})`)
)

// ViteDevPort resolves the Vite dev-server port: an explicit --port in the dev
// script wins, then a `port:` in the vite config (best-effort), else Vite's
// default (5173).
func ViteDevPort(root string) int {
	if data, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		var pj struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &pj) == nil {
			if m := devPortFlagRe.FindStringSubmatch(pj.Scripts["dev"]); m != nil {
				if p, _ := strconv.Atoi(m[1]); p > 0 {
					return p
				}
			}
		}
	}
	for _, n := range viteConfigNames {
		if data, err := os.ReadFile(filepath.Join(root, n)); err == nil {
			if m := configPortRe.FindStringSubmatch(string(data)); m != nil {
				if p, _ := strconv.Atoi(m[1]); p > 0 {
					return p
				}
			}
		}
	}
	return viteDefaultPort
}
