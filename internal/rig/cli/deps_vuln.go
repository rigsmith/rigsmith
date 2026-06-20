package cli

import (
	"encoding/json"
	"strings"

	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
	"github.com/spf13/cobra"
)

// Vulnerability scanning for `rig deps --vulnerable`: each ecosystem's audit
// report is parsed into a key→highest-severity map that the command overlays
// onto the dependency rows. Keys match the dep rows: project+name for .NET
// (where the same package can differ per project), name alone elsewhere. The
// parsers are pure; auditSeverities does the exec.

// severityRank orders advisory severities so the highest can win when a package
// has several. Unknown/empty rank below everything. Pure.
func severityRank(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return 4
	case "high":
		return 3
	case "moderate", "medium":
		return 2
	case "low":
		return 1
	case "info", "information", "informational":
		return 0
	default:
		return -1
	}
}

// maxSeverity returns the higher-ranked of two severities (canonicalized to the
// incoming spelling of the winner). Pure.
func maxSeverity(a, b string) string {
	if severityRank(b) > severityRank(a) {
		return b
	}
	return a
}

// depKey is the overlay key for a dep: project+name for .NET, name elsewhere.
// Shared by the latest- and vulnerability-overlays. Pure.
func depKey(d outdatedDep, byProject bool) string {
	if byProject {
		return d.project + "\x00" + d.name
	}
	return d.name
}

// parseDotnetVulnerable parses `dotnet list package --vulnerable --format json`
// into a (project+name)→highest-severity map. Pure.
func parseDotnetVulnerable(jsonText string) map[string]string {
	var doc struct {
		Projects []struct {
			Path       string `json:"path"`
			Frameworks []struct {
				TopLevelPackages []struct {
					ID              string `json:"id"`
					Vulnerabilities []struct {
						Severity string `json:"severity"`
					} `json:"vulnerabilities"`
				} `json:"topLevelPackages"`
			} `json:"frameworks"`
		} `json:"projects"`
	}
	if json.Unmarshal([]byte(jsonText), &doc) != nil {
		return nil
	}
	out := map[string]string{}
	for _, p := range doc.Projects {
		for _, f := range p.Frameworks {
			for _, pkg := range f.TopLevelPackages {
				for _, v := range pkg.Vulnerabilities {
					key := p.Path + "\x00" + pkg.ID
					out[key] = maxSeverity(out[key], v.Severity)
				}
			}
		}
	}
	return out
}

// parseNpmAudit parses `npm audit --json` (npm v7+: a `vulnerabilities` object
// keyed by package name with a `severity`) into a name→severity map. Pure.
func parseNpmAudit(jsonText string) map[string]string {
	var doc struct {
		Vulnerabilities map[string]struct {
			Severity string `json:"severity"`
		} `json:"vulnerabilities"`
	}
	if json.Unmarshal([]byte(jsonText), &doc) != nil {
		return nil
	}
	out := map[string]string{}
	for name, v := range doc.Vulnerabilities {
		out[name] = maxSeverity(out[name], v.Severity)
	}
	return out
}

// parsePnpmAudit parses `pnpm audit --json` (the npm-v6 shape: an `advisories`
// object keyed by advisory id, each with `module_name` + `severity`) into a
// name→highest-severity map. Pure.
func parsePnpmAudit(jsonText string) map[string]string {
	var doc struct {
		Advisories map[string]struct {
			ModuleName string `json:"module_name"`
			Severity   string `json:"severity"`
		} `json:"advisories"`
	}
	if json.Unmarshal([]byte(jsonText), &doc) != nil {
		return nil
	}
	out := map[string]string{}
	for _, a := range doc.Advisories {
		if a.ModuleName == "" {
			continue
		}
		out[a.ModuleName] = maxSeverity(out[a.ModuleName], a.Severity)
	}
	return out
}

// parseBunAudit parses `bun audit --json` (an object keyed by package name whose
// value is an array of advisories, each with a `severity`) into a name→highest-
// severity map. Pure.
func parseBunAudit(jsonText string) map[string]string {
	var doc map[string][]struct {
		Severity string `json:"severity"`
	}
	if json.Unmarshal([]byte(jsonText), &doc) != nil {
		return nil
	}
	out := map[string]string{}
	for name, advisories := range doc {
		for _, a := range advisories {
			out[name] = maxSeverity(out[name], a.Severity)
		}
	}
	return out
}

// parseYarnClassicAudit parses yarn v1's `yarn audit --json` — NDJSON whose
// `{"type":"auditAdvisory",...}` messages carry `data.advisory.{module_name,
// severity}` — into a name→highest-severity map. Pure.
func parseYarnClassicAudit(text string) map[string]string {
	out := map[string]string{}
	dec := json.NewDecoder(strings.NewReader(text))
	for {
		var msg struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := dec.Decode(&msg); err != nil {
			break
		}
		if msg.Type != "auditAdvisory" {
			continue
		}
		var data struct {
			Advisory struct {
				ModuleName string `json:"module_name"`
				Severity   string `json:"severity"`
			} `json:"advisory"`
		}
		if json.Unmarshal(msg.Data, &data) != nil || data.Advisory.ModuleName == "" {
			continue
		}
		name := data.Advisory.ModuleName
		out[name] = maxSeverity(out[name], data.Advisory.Severity)
	}
	return out
}

// auditSeverities runs the ecosystem's audit and returns a key→severity map
// (key per depKey). supported=false means audit isn't wired for this toolchain
// (go/cargo need separate scanners; yarn berry has no compatible report) — the
// caller notes it and omits the column.
func auditSeverities(cmd *cobra.Command, eco, root string) (sev map[string]string, supported bool) {
	switch eco {
	case detect.DotNet:
		cfg, _ := config.LoadMerged(root)
		target, terr := dotnetListTarget(root, cfg)
		if terr != nil {
			return nil, false // no target — audit column omitted, like other unwired toolchains
		}
		// Audit commands report findings and exit non-zero by design; the JSON on
		// stdout is still valid, so the error is ignored when there's output.
		out, err := captureOutdated(cmd, root, "dotnet", "list", target, "package", "--vulnerable", "--format", "json")
		if err != nil && out == "" {
			return nil, false
		}
		return parseDotnetVulnerable(out), true
	case detect.Node:
		switch pm := detect.DetectNodePM(root); pm {
		case detect.NPM:
			out, _ := captureOutdated(cmd, root, "npm", "audit", "--json")
			return parseNpmAudit(out), true
		case detect.PNPM:
			out, _ := captureOutdated(cmd, root, "pnpm", "audit", "--json")
			return parsePnpmAudit(out), true
		case detect.Bun:
			out, _ := captureOutdated(cmd, root, "bun", "audit", "--json")
			return parseBunAudit(out), true
		case detect.Yarn:
			if yarnIsBerry(cmd, root) {
				return nil, false // berry's `yarn npm audit` isn't wired yet
			}
			out, _ := captureOutdated(cmd, root, "yarn", "audit", "--json")
			return parseYarnClassicAudit(out), true
		default:
			return nil, false
		}
	default:
		return nil, false // go (govulncheck) / cargo (cargo-audit) not wired
	}
}

// applyVulnerabilities overlays the audit severities onto the dep rows in place,
// keyed per byProject. Returns the count of rows that gained a severity.
func applyVulnerabilities(rows []outdatedDep, sev map[string]string, byProject bool) int {
	n := 0
	for i := range rows {
		if s := sev[depKey(rows[i], byProject)]; s != "" {
			rows[i].vuln = s
			n++
		}
	}
	return n
}
