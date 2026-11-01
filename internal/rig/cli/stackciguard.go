package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// Demotion keeps diagnostics observable. The guard, rather than NoWarn, makes
// the final decision; a warning whose package cannot be established fails closed.
func newStackCIGuardCmd() *cobra.Command {
	var strict bool
	c := &cobra.Command{Use: "ci-guard <project.csproj>", Short: "Build a .NET project with an observed stack warning guard", Long: "Builds a .NET project with TreatWarningsAsErrors. In plain clones only\nStackOnlyWarning items are held back. In stacks only observed NU1603, NU1608\nand NU1605 diagnostics for rewired package identities are eligible. Sibling\ncompiler warnings remain visible and counted; --strict makes them fatal.\nMember WarningsNotAsErrors entries are retained. Other warnings fail the\nguard. MSB3277 remains an error because assembly scope cannot\nbe proven. Writes observed diagnostics to GITHUB_STEP_SUMMARY. Use --strict\nto disable the guard. Requires the dotnet SDK; accepts one project, not a solution.", Args: cobra.ExactArgs(1)}
	_ = c.MarkZshCompPositionalArgumentFile(1, "*.csproj")
	c.Flags().BoolVar(&strict, "strict", false, "Keep all warnings as errors")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		project, e := filepath.Abs(args[0])
		if e != nil {
			return e
		}
		run := func(args ...string) ([]byte, error) {
			p := exec.CommandContext(cmd.Context(), "dotnet", args...)
			p.Env = append(os.Environ(), "DOTNET_CLI_UI_LANGUAGE=en-US", "VSLANG=1033")
			return p.CombinedOutput()
		}
		var meta stackGuardMetadata
		out, e := run("msbuild", project, "-nologo", "-p:TreatWarningsAsErrors=true", "-getItem:StackOnlyWarning,_StackPresent", "-getProperty:RigStackspace,RigStackspaceRoot,ProjectAssetsFile,WarningsAsErrors,WarningsNotAsErrors")
		if e != nil {
			return fmt.Errorf("evaluate guard: %w\n%s", e, out)
		}
		if e = json.Unmarshal(out, &meta); e != nil {
			return fmt.Errorf("evaluate guard metadata: %w", e)
		}
		if meta.Properties.RigStackspace == "true" {
			rel, err := filepath.Rel(meta.Properties.RigStackspaceRoot, project)
			if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("project is outside the stackspace")
			}
			meta.MemberRoot = filepath.Join(meta.Properties.RigStackspaceRoot, strings.Split(rel, string(filepath.Separator))[0]) + string(filepath.Separator)
		}
		buildArgs := []string{"build", project, "--no-incremental", "-nologo", "-v:minimal", "-clp:NoSummary", "-tl:off", "-p:TreatWarningsAsErrors=true"}
		if strict {
			buildArgs = append(buildArgs, "-p:RigStackCIGuard=false")
		}
		if !strict {
			var eligible []string
			if meta.Properties.RigStackspace == "true" && len(meta.Items.Present) > 0 {
				eligible = []string{"NU1603", "NU1608", "NU1605"}
			} else if meta.Properties.RigStackspace != "true" {
				for _, item := range meta.Items.StackOnlyWarning {
					eligible = append(eligible, item.Identity)
				}
			}
			var remaining []string
			for _, code := range strings.Split(meta.Properties.WarningsAsErrors, ";") {
				keep := true
				for _, id := range eligible {
					if strings.EqualFold(strings.TrimSpace(code), id) {
						keep = false
					}
				}
				if keep && strings.TrimSpace(code) != "" {
					remaining = append(remaining, strings.TrimSpace(code))
				}
			}
			buildArgs = append(buildArgs, "-p:WarningsAsErrors="+strings.Join(remaining, "%3B"))
			buildArgs = append(buildArgs, "-p:RigStackCIGuard=true")
			if meta.MemberRoot != "" {
				buildArgs = append(buildArgs, "-p:RigStackCIMemberRoot="+meta.MemberRoot)
			}
			if meta.Properties.RigStackspace == "true" && len(meta.Items.Present) > 0 {
				buildArgs = append(buildArgs, "-p:WarningsNotAsErrors="+strings.Join(append(strings.Split(meta.Properties.WarningsNotAsErrors, ";"), "NU1603", "NU1608", "NU1605"), "%3B"))
			}
			if meta.Properties.RigStackspace != "true" {
				var ids []string
				for _, item := range meta.Items.StackOnlyWarning {
					if !stackGuardCode.MatchString(item.Identity) {
						return fmt.Errorf("invalid StackOnlyWarning id %q", item.Identity)
					}
					ids = append(ids, item.Identity)
				}
				if len(ids) > 0 {
					buildArgs = append(buildArgs, "-p:WarningsNotAsErrors="+strings.Join(append(strings.Split(meta.Properties.WarningsNotAsErrors, ";"), ids...), "%3B"))
				}
			}
		}
		out, buildErr := run(buildArgs...)
		held, unexpected := stackGuardOutput(string(out), project, meta, strict)
		meta.MemberTolerances = map[string]string{project: meta.Properties.WarningsNotAsErrors}
		if !strict && meta.MemberRoot != "" {
			for _, line := range unexpected {
				m := stackGuardDiagnostic.FindStringSubmatch(line)
				if len(m) == 0 {
					continue
				}
				origin := filepath.Clean(strings.Split(m[4], "::")[0])
				if !insideRoot(meta.MemberRoot, origin) {
					continue
				}
				if _, ok := meta.MemberTolerances[origin]; ok {
					continue
				}
				data, err := run("msbuild", origin, "-nologo", "-p:TreatWarningsAsErrors=true", "-getProperty:WarningsNotAsErrors,WarningsAsErrors")
				if err != nil {
					return fmt.Errorf("evaluate member warning policy: %w\n%s", err, data)
				}
				var policy stackGuardMetadata
				if err = json.Unmarshal(data, &policy); err != nil {
					return err
				}
				meta.MemberTolerances[origin] = policy.Properties.WarningsNotAsErrors
			}
		}
		for _, line := range unexpected {
			// The raw total includes events reported separately below. Printing it as
			// the guarded warning total would misrepresent the result.
			if len(held) > 0 && stackGuardWarningTotal.MatchString(line) {
				continue
			}
			fmt.Fprintln(cmd.OutOrStdout(), line)
		}
		siblings, tolerated, fatal := stackGuardVisiblePolicy(unexpected, project, meta, strict)
		report := fmt.Sprintf("%d stack-only warnings held back", len(held))
		if len(held) > 0 {
			report += ":\n\n"
			for _, line := range held {
				report += "    " + line + "\n"
			}
		}
		if len(siblings) > 0 {
			report += fmt.Sprintf("\n%d sibling warnings observed (non-fatal):\n", len(siblings))
			for _, line := range siblings {
				report += "    " + line + "\n"
			}
		}
		if len(tolerated) > 0 {
			report += fmt.Sprintf("\n%d member-policy warnings observed (non-fatal):\n", len(tolerated))
			for _, line := range tolerated {
				report += "    " + line + "\n"
			}
		}
		fmt.Fprintln(cmd.OutOrStdout(), report)
		if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, err = f.WriteString("\n" + report + "\n")
			f.Close()
			if err != nil {
				return err
			}
		}
		if buildErr != nil {
			return fmt.Errorf("guarded build: %w", buildErr)
		}
		if fatal {
			return fmt.Errorf("unguarded warning remains")
		}
		return nil
	}
	return c
}

type stackGuardItem struct{ Identity string }
type stackGuardMetadata struct {
	MemberRoot       string
	MemberTolerances map[string]string
	Properties       struct{ RigStackspace, RigStackspaceRoot, ProjectAssetsFile, WarningsAsErrors, WarningsNotAsErrors string }
	Items            struct {
		StackOnlyWarning []stackGuardItem
		Present          []stackGuardItem `json:"_StackPresent"`
	}
}

var stackGuardWarningTotal = regexp.MustCompile(`^\s*[0-9]+ Warning\(s\)\s*$`)

var stackGuardCode = regexp.MustCompile(`^[A-Z][A-Z0-9]*[0-9]$`)
var stackGuardDiagnostic = regexp.MustCompile(`^(.*): warning ([A-Z]+[0-9]+): (.*?)(?: \[([^\]]+)\])?$`)

func stackGuardOutput(output, project string, meta stackGuardMetadata, strict bool) (held, visible []string) {
	type diagnostic struct {
		lines                  []string
		code, message, project string
	}
	var records []diagnostic
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		m := stackGuardDiagnostic.FindStringSubmatch(line)
		if len(m) == 0 {
			records = append(records, diagnostic{lines: []string{line}})
			continue
		}
		origin := strings.TrimSpace(m[4])
		if origin == "" {
			origin = strings.TrimSpace(m[1])
		}
		origin = strings.Split(origin, "::")[0]
		if len(records) > 0 && strings.HasPrefix(m[3], " ") {
			last := &records[len(records)-1]
			if last.code == m[2] && last.project == origin {
				last.message += "\n" + m[3]
				last.lines = append(last.lines, line)
				continue
			}
		}
		records = append(records, diagnostic{lines: []string{line}, code: m[2], message: m[3], project: origin})
	}
	seen := map[string]bool{}
	for _, record := range records {
		allowed := false
		if !strict && record.code != "" && filepath.Clean(record.project) == project {
			if meta.Properties.RigStackspace != "true" {
				for _, item := range meta.Items.StackOnlyWarning {
					allowed = allowed || item.Identity == record.code
				}
			} else if len(meta.Items.Present) > 0 {
				allowed = stackGuardNuGet(meta, record.code, record.message)
			}
		}
		if allowed {
			line := strings.Join(record.lines, "\n")
			if !seen[line] {
				held = append(held, line)
				seen[line] = true
			}
		} else {
			visible = append(visible, record.lines...)
		}
	}
	return
}

func stackGuardNuGet(meta stackGuardMetadata, code, message string) bool {
	if code != "NU1603" && code != "NU1608" && code != "NU1605" {
		return false
	}
	var assets struct {
		Logs []struct{ Code, LibraryID, Message string }
	}
	b, e := os.ReadFile(meta.Properties.ProjectAssetsFile)
	if e != nil || json.Unmarshal(b, &assets) != nil {
		return false
	}
	for _, log := range assets.Logs {
		if log.Code != code || strings.TrimSpace(log.Message) != strings.TrimSpace(message) {
			continue
		}
		for _, item := range meta.Items.Present {
			if strings.EqualFold(item.Identity, log.LibraryID) {
				return true
			}
		}
	}
	return false
}

var stackGuardCompilerCode = regexp.MustCompile(`^(CS|FS|BC)[0-9]+$`)

// Sibling compiler warnings remain in the output. Assembly/restore warnings
// still require attribution; an unrelated MSB3277 must never become harmless.
func stackGuardVisiblePolicy(lines []string, project string, meta stackGuardMetadata, strict bool) (siblings, tolerated []string, fatal bool) {
	seen := map[string]bool{}
	for _, line := range lines {
		if !strings.Contains(line, ": warning ") {
			continue
		}
		m := stackGuardDiagnostic.FindStringSubmatch(line)
		if strict || len(m) == 0 {
			fatal = true
			continue
		}
		origin := filepath.Clean(strings.Split(m[4], "::")[0])
		if stackGuardCompilerCode.MatchString(m[2]) && meta.MemberRoot != "" && insideRoot(meta.Properties.RigStackspaceRoot, origin) && !insideRoot(meta.MemberRoot, origin) {
			if !seen[line] {
				siblings = append(siblings, line)
				seen[line] = true
			}
			continue
		}
		allowed := false
		policy, known := meta.MemberTolerances[origin]
		if origin == project {
			policy = meta.Properties.WarningsNotAsErrors
			known = true
		}
		if known {
			for _, code := range strings.Split(policy, ";") {
				if strings.EqualFold(strings.TrimSpace(code), m[2]) {
					allowed = true
				}
			}
		}
		if allowed {
			if !seen[line] {
				tolerated = append(tolerated, line)
				seen[line] = true
			}
		} else {
			fatal = true
		}
	}
	return
}
