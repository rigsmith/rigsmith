package sign

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// windowsSignableExts are the artifact extensions Windows Authenticode signing
// applies to. Installers (.exe/.msi/.msix/.appx) and PE binaries (.exe/.dll) are
// signable; archives, .deb/.AppImage/.dmg and updater manifests are not.
var windowsSignableExts = map[string]bool{
	".exe":  true,
	".msi":  true,
	".dll":  true,
	".msix": true,
	".appx": true,
	".cab":  true,
}

// SignableWindows returns the subset of arts that are Windows-signable.
func SignableWindows(arts []plugin.Artifact) []plugin.Artifact {
	var out []plugin.Artifact
	for _, a := range arts {
		if windowsSignableExts[strings.ToLower(filepath.Ext(a.Path))] {
			out = append(out, a)
		}
	}
	return out
}

// defaultTimestampURL is Azure Trusted Signing's RFC-3161 timestamp server.
const defaultTimestampURL = "http://timestamp.acs.microsoft.com"

// WindowsCommands builds the signer invocation(s) for files under cfg — one
// command per file, so each is deterministic and independently reportable. It is
// exported so the sign step can preview the commands in a dry run and tests can
// assert them without executing. The azure-trusted-signing tool drives the
// dotnet `sign` CLI (`sign code trusted-signing`); the "command" tool runs cfg's
// own argv with "{file}" substituted.
func WindowsCommands(cfg *config.WindowsSigning, files []string) ([][]string, error) {
	if cfg == nil || len(files) == 0 {
		return nil, nil
	}
	tool := cfg.Tool
	if tool == "" {
		tool = "azure-trusted-signing"
	}

	var cmds [][]string
	switch tool {
	case "azure-trusted-signing":
		if cfg.Endpoint == "" || cfg.Account == "" || cfg.CertProfile == "" {
			return nil, fmt.Errorf("windows signing: azure-trusted-signing needs endpoint, account, and certificateProfile")
		}
		ts := cfg.TimestampURL
		if ts == "" {
			ts = defaultTimestampURL
		}
		for _, f := range files {
			// `sign` matches a file pattern relative to a base dir (-b); pass each
			// file's directory + basename so exactly that artifact is signed.
			cmds = append(cmds, []string{
				"sign", "code", "trusted-signing",
				"-b", filepath.Dir(f),
				"-tse", cfg.Endpoint,
				"-tsa", cfg.Account,
				"-tscp", cfg.CertProfile,
				"-t", ts,
				filepath.Base(f),
			})
		}
	case "command":
		if len(cfg.Command) == 0 {
			return nil, fmt.Errorf("windows signing: tool \"command\" requires a non-empty command")
		}
		for _, f := range files {
			argv := make([]string, len(cfg.Command))
			for i, a := range cfg.Command {
				argv[i] = strings.ReplaceAll(a, "{file}", f)
			}
			cmds = append(cmds, argv)
		}
	default:
		return nil, fmt.Errorf("windows signing: unknown tool %q (want \"azure-trusted-signing\" or \"command\")", tool)
	}
	return cmds, nil
}

// SignWindows signs the Windows-signable artifacts under cfg. extraEnv carries
// the resolved (and already-masked) signing secrets — e.g. the AZURE_* service-
// principal credentials the `sign` CLI's DefaultAzureCredential reads — layered
// onto baseEnv for the signer process. With dryRun it returns the commands it
// would run without executing or contacting the signing service. It returns the
// files it signed and a combined output string for the caller to surface (the
// caller's reporter masks secrets); a no-op (nil cfg or no signable files)
// returns empties.
func SignWindows(ctx context.Context, arts []plugin.Artifact, cfg *config.WindowsSigning, extraEnv map[string]string, baseEnv []string, dryRun bool) (signed []string, output string, err error) {
	if cfg == nil {
		return nil, "", nil
	}
	signable := SignableWindows(arts)
	if len(signable) == 0 {
		return nil, "", nil
	}
	files := make([]string, 0, len(signable))
	for _, a := range signable {
		files = append(files, a.Path)
	}
	sort.Strings(files)

	cmds, err := WindowsCommands(cfg, files)
	if err != nil {
		return nil, "", err
	}

	env := mergeEnv(baseEnv, extraEnv)
	var b strings.Builder
	for i, argv := range cmds {
		if dryRun {
			fmt.Fprintf(&b, "would sign %s: %s\n", filepath.Base(files[i]), strings.Join(argv, " "))
			signed = append(signed, files[i])
			continue
		}
		out, runErr := runEnv(ctx, env, argv)
		if out != "" {
			b.WriteString(out)
		}
		if runErr != nil {
			return signed, b.String(), fmt.Errorf("windows signing %s: %w", filepath.Base(files[i]), runErr)
		}
		signed = append(signed, files[i])
	}
	return signed, b.String(), nil
}

// mergeEnv layers extra (KEY->VALUE) onto base. A nil base inherits the parent
// process environment, so the signer still finds its PATH/credentials.
func mergeEnv(base []string, extra map[string]string) []string {
	if base == nil {
		base = os.Environ()
	}
	if len(extra) == 0 {
		return base
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := append([]string{}, base...)
	for _, k := range keys {
		out = append(out, k+"="+extra[k])
	}
	return out
}

func runEnv(ctx context.Context, env []string, argv []string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}
