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

// defaultTimestampURL is Azure Trusted Signing's RFC-3161 timestamp server.
const defaultTimestampURL = "http://timestamp.acs.microsoft.com"

// matchesSigner reports whether an artifact path's extension is in the signer's
// (case-insensitive) extension set.
func matchesSigner(s config.Signer, path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range s.Extensions {
		if strings.ToLower(e) == ext {
			return true
		}
	}
	return false
}

// SignerCommands builds the invocation(s) for a signer over files — one command
// per file, so each is deterministic and independently reportable. Exported so
// the sign step can preview them in a dry run and tests can assert them without
// executing. The "command" tool substitutes "{file}" in the configured argv; the
// "azure-trusted-signing" preset drives the dotnet `sign` CLI.
func SignerCommands(s config.Signer, files []string) ([][]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	tool := s.Tool
	if tool == "" {
		tool = "command"
	}

	var cmds [][]string
	switch tool {
	case "command":
		if len(s.Command) == 0 {
			return nil, fmt.Errorf("signing: a \"command\" signer needs a non-empty command")
		}
		for _, f := range files {
			argv := make([]string, len(s.Command))
			for i, a := range s.Command {
				argv[i] = strings.ReplaceAll(a, "{file}", f)
			}
			cmds = append(cmds, argv)
		}
	case "azure-trusted-signing":
		if s.Endpoint == "" || s.Account == "" || s.CertProfile == "" {
			return nil, fmt.Errorf("signing: azure-trusted-signing needs endpoint, account, and certificateProfile")
		}
		ts := s.TimestampURL
		if ts == "" {
			ts = defaultTimestampURL
		}
		for _, f := range files {
			// `sign` matches a file pattern relative to a base dir (-b); pass each
			// file's directory + basename so exactly that artifact is signed.
			cmds = append(cmds, []string{
				"sign", "code", "trusted-signing",
				"-b", filepath.Dir(f),
				"-tse", s.Endpoint,
				"-tsa", s.Account,
				"-tscp", s.CertProfile,
				"-t", ts,
				filepath.Base(f),
			})
		}
	default:
		return nil, fmt.Errorf("signing: unknown tool %q (want \"command\" or \"azure-trusted-signing\")", tool)
	}
	return cmds, nil
}

// Apply runs every signer over the artifacts it matches (by extension). extraEnv
// carries the resolved (already-masked) signing secrets — e.g. AZURE_* service-
// principal creds the `sign` CLI's DefaultAzureCredential reads, or whatever a
// custom command needs — layered onto baseEnv for the signer process. With dryRun
// it returns the commands it would run without executing or contacting any
// signing service. It returns the files it signed and a combined output string
// for the caller to surface (the caller's reporter masks secrets); a no-op (no
// signers or no matching artifacts) returns empties.
func Apply(ctx context.Context, arts []plugin.Artifact, signers []config.Signer, extraEnv map[string]string, baseEnv []string, dryRun bool) (signed []string, output string, err error) {
	if len(signers) == 0 {
		return nil, "", nil
	}
	env := mergeEnv(baseEnv, extraEnv)
	var b strings.Builder
	for _, s := range signers {
		var files []string
		for _, a := range arts {
			if matchesSigner(s, a.Path) {
				files = append(files, a.Path)
			}
		}
		sort.Strings(files)
		if len(files) == 0 {
			continue
		}
		cmds, cerr := SignerCommands(s, files)
		if cerr != nil {
			return signed, b.String(), cerr
		}
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
				return signed, b.String(), fmt.Errorf("signing %s: %w", filepath.Base(files[i]), runErr)
			}
			signed = append(signed, files[i])
		}
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
