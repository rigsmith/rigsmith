package cli

import (
	"encoding/json"
	"strings"
)

// The parsers here read an ecosystem's "list every installed dependency"
// report into outdatedDep rows carrying name + current (latest left empty —
// `rig deps` fills it by overlaying the outdated report). They mirror the
// outdated parsers in outdated_parse.go but keep up-to-date packages too, which
// the outdated reports drop. All pure.

// parseGoListAll parses `go list -m -u -json all` into every (non-main) module
// with its current version and, when present, the available update as latest.
// Up-to-date modules get latest == current. Pure.
func parseGoListAll(stream string) []outdatedDep {
	type mod struct {
		Path    string
		Version string
		Main    bool
		Update  *struct {
			Version string
		}
	}
	dec := json.NewDecoder(strings.NewReader(stream))
	var out []outdatedDep
	for {
		var m mod
		if err := dec.Decode(&m); err != nil {
			break // EOF or malformed tail — return what parsed
		}
		if m.Main || m.Version == "" {
			continue // skip the main module(s) and replace-only/pseudo entries
		}
		latest := m.Version
		if m.Update != nil && m.Update.Version != "" {
			latest = m.Update.Version
		}
		out = append(out, outdatedDep{name: m.Path, current: m.Version, latest: latest})
	}
	sortDeps(out)
	return out
}

// parseDotnetList parses `dotnet list package --format json` into every
// top-level package with its resolved (current) version, deduped per project +
// framework and keeping the owning project path. latest is left empty for the
// caller to overlay from the outdated report. Pure.
func parseDotnetList(jsonText string) []outdatedDep {
	var doc struct {
		Projects []struct {
			Path       string `json:"path"`
			Frameworks []struct {
				TopLevelPackages []struct {
					ID              string `json:"id"`
					ResolvedVersion string `json:"resolvedVersion"`
				} `json:"topLevelPackages"`
			} `json:"frameworks"`
		} `json:"projects"`
	}
	if json.Unmarshal([]byte(jsonText), &doc) != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []outdatedDep
	for _, p := range doc.Projects {
		for _, f := range p.Frameworks {
			for _, pkg := range f.TopLevelPackages {
				if pkg.ID == "" {
					continue
				}
				key := p.Path + "\x00" + pkg.ID
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, outdatedDep{
					name:    pkg.ID,
					current: pkg.ResolvedVersion,
					project: p.Path,
				})
			}
		}
	}
	sortDeps(out)
	return out
}

// parseNpmList parses `npm ls --json --depth=0` — an object with a
// `dependencies` map keyed by name (npm folds prod and dev together here) —
// into every top-level package with its installed version. Pure.
func parseNpmList(jsonText string) []outdatedDep {
	var doc struct {
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}
	if json.Unmarshal([]byte(jsonText), &doc) != nil {
		return nil
	}
	var out []outdatedDep
	for name, v := range doc.Dependencies {
		out = append(out, outdatedDep{name: name, current: v.Version})
	}
	sortDeps(out)
	return out
}

// parsePnpmList parses `pnpm ls --json --depth=0` — an array of workspace
// projects, each with separate `dependencies` and `devDependencies` maps — into
// every top-level package with its installed version (dev recorded). The first
// (root) project is used. Pure.
func parsePnpmList(jsonText string) []outdatedDep {
	type pkgMap map[string]struct {
		Version string `json:"version"`
	}
	var docs []struct {
		Dependencies    pkgMap `json:"dependencies"`
		DevDependencies pkgMap `json:"devDependencies"`
	}
	if json.Unmarshal([]byte(jsonText), &docs) != nil || len(docs) == 0 {
		return nil
	}
	var out []outdatedDep
	for name, v := range docs[0].Dependencies {
		out = append(out, outdatedDep{name: name, current: v.Version})
	}
	for name, v := range docs[0].DevDependencies {
		out = append(out, outdatedDep{name: name, current: v.Version, dev: true})
	}
	sortDeps(out)
	return out
}

// parseBunList parses `bun pm ls` — a text tree (bun has no JSON here) whose
// rows look like "├── name@version" / "└── name@version" — into every top-level
// package. Scoped names keep their leading @, so the version is split at the
// LAST @. The first line is the project banner and is skipped. Pure.
func parseBunList(text string) []outdatedDep {
	var out []outdatedDep
	for _, raw := range strings.Split(stripANSI(text), "\n") {
		line := strings.TrimSpace(raw)
		spec := line
		// Strip a leading tree connector (├──, └──, │) if present.
		for _, p := range []string{"├──", "└──", "├─", "└─", "│"} {
			if strings.HasPrefix(spec, p) {
				spec = strings.TrimSpace(spec[len(p):])
				break
			}
		}
		if spec == line {
			continue // no tree connector → banner / blank, not a dependency row
		}
		at := strings.LastIndex(spec, "@")
		if at <= 0 { // no version, or leading-@ scope with no version
			continue
		}
		name, version := spec[:at], spec[at+1:]
		if name == "" || version == "" {
			continue
		}
		out = append(out, outdatedDep{name: name, current: version})
	}
	sortDeps(out)
	return out
}

// parseYarnClassicList parses yarn v1's `yarn list --depth=0 --json` — NDJSON
// whose `{"type":"tree",...}` message carries `data.trees[].name` as
// "pkg@range-or-version". The name is split at the LAST @ (scoped names keep
// their leading @). Pure.
func parseYarnClassicList(text string) []outdatedDep {
	var out []outdatedDep
	dec := json.NewDecoder(strings.NewReader(text))
	for {
		var msg struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := dec.Decode(&msg); err != nil {
			break
		}
		if msg.Type != "tree" {
			continue
		}
		var data struct {
			Trees []struct {
				Name string `json:"name"`
			} `json:"trees"`
		}
		if json.Unmarshal(msg.Data, &data) != nil {
			continue
		}
		for _, t := range data.Trees {
			at := strings.LastIndex(t.Name, "@")
			if at <= 0 {
				continue
			}
			name, version := t.Name[:at], t.Name[at+1:]
			if name == "" || version == "" {
				continue
			}
			out = append(out, outdatedDep{name: name, current: version})
		}
	}
	sortDeps(out)
	return out
}

// mergeLatest overlays the latest versions from an outdated report onto the full
// dependency list: each dep's latest becomes its outdated-report latest when it
// has an update, else its own current (it's up to date). byProject keys the
// overlay on project+name (for .NET, where the same package can differ per
// project); otherwise on name alone. Pure.
func mergeLatest(all, outdated []outdatedDep, byProject bool) []outdatedDep {
	key := func(d outdatedDep) string {
		if byProject {
			return d.project + "\x00" + d.name
		}
		return d.name
	}
	latest := make(map[string]string, len(outdated))
	for _, d := range outdated {
		latest[key(d)] = d.latest
	}
	out := make([]outdatedDep, len(all))
	for i, d := range all {
		if l, ok := latest[key(d)]; ok && l != "" {
			d.latest = l
		} else {
			d.latest = d.current
		}
		out[i] = d
	}
	sortDeps(out)
	return out
}
