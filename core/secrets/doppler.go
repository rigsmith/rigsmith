package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// DefaultStepID is the step id the generated CI fetch step takes, so
// `steps.doppler.outputs.NAME` is what a consuming step names.
const DefaultStepID = "doppler"

// Doppler is the Doppler provider. It runs the doppler CLI with the project
// and config named on every call (-p/-c), so it works in a fresh worktree
// with no `doppler setup` there.
type Doppler struct {
	// LookPath finds the CLI; exec.LookPath in production.
	LookPath func(file string) (string, error)
	// Run runs the CLI in dir and returns its stdout and stderr separately,
	// so a secret on stdout is never mixed into a diagnostic.
	Run func(ctx context.Context, dir string, args ...string) (stdout, stderr []byte, err error)
	// Getenv reads DOPPLER_TOKEN, which signs the CLI in without a login.
	Getenv func(string) string
}

// NewDoppler returns the provider wired to the real CLI.
func NewDoppler() *Doppler {
	return &Doppler{LookPath: exec.LookPath, Run: runCLI, Getenv: os.Getenv}
}

func runCLI(ctx context.Context, dir string, args ...string) ([]byte, []byte, error) {
	c := exec.CommandContext(ctx, "doppler", args...)
	c.Dir = dir
	var out, errBuf bytes.Buffer
	c.Stdout, c.Stderr = &out, &errBuf
	err := c.Run()
	return out.Bytes(), errBuf.Bytes(), err
}

func (d *Doppler) Name() string  { return "doppler" }
func (d *Doppler) Title() string { return "Doppler" }

// Ready checks the CLI is on PATH and has a token for dir: DOPPLER_TOKEN, or
// the one `doppler login` stored for this folder or a parent. The check is
// local; a token Doppler has since revoked fails at the fetch, with Doppler's
// own error, never as a run without secrets.
func (d *Doppler) Ready(ctx context.Context, dir string) error {
	if _, err := d.LookPath("doppler"); err != nil {
		return fmt.Errorf("the doppler CLI is not installed — install it (`brew install dopplerhq/cli/doppler`, "+
			"or https://docs.doppler.com/docs/install-cli), then run `doppler login`: %w", ErrNotInstalled)
	}
	if strings.TrimSpace(d.Getenv("DOPPLER_TOKEN")) != "" {
		return nil
	}
	// The token goes to a buffer that is only measured, never printed.
	out, _, err := d.Run(ctx, dir, "configure", "get", "token", "--plain", "--no-check-version", "--scope", dir)
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return fmt.Errorf("the doppler CLI is not signed in — run `doppler login`: %w", ErrSignedOut)
	}
	return nil
}

// Wrap runs argv under `doppler run`, which injects the scope's secrets into
// argv's environment and nothing else's.
func (d *Doppler) Wrap(scope Scope, argv []string) []string {
	return append([]string{"doppler", "run", "-p", scope.Project, "-c", scope.Config, "--"}, argv...)
}

// Get reads one secret with `doppler secrets get NAME --plain`.
func (d *Doppler) Get(ctx context.Context, dir string, scope Scope, name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("%q is not a secret name (letters, digits and '_')", name)
	}
	out, stderr, err := d.Run(ctx, dir, "secrets", "get", name, "--plain", "--no-check-version",
		"-p", scope.Project, "-c", scope.Config)
	if err != nil {
		if strings.Contains(strings.ToLower(string(stderr)), "could not find requested secret") {
			return "", fmt.Errorf("Doppler %s has no secret %s: %w", scope, name, ErrNotFound)
		}
		return "", fmt.Errorf("reading %s from Doppler %s failed: %s", name, scope, diagnostic(stderr, err))
	}
	v := strings.TrimRight(string(out), "\r\n")
	if strings.TrimSpace(v) == "" {
		return "", fmt.Errorf("Doppler %s has %s set to nothing: %w", scope, name, ErrEmpty)
	}
	return v, nil
}

// All reads every secret in scope with `doppler secrets download --no-file`,
// which writes the JSON to stdout and nothing to disk.
func (d *Doppler) All(ctx context.Context, dir string, scope Scope) (map[string]string, error) {
	out, stderr, err := d.Run(ctx, dir, "secrets", "download", "--no-file", "--format", "json", "--no-check-version",
		"-p", scope.Project, "-c", scope.Config)
	if err != nil {
		return nil, fmt.Errorf("reading Doppler %s failed: %s", scope, diagnostic(stderr, err))
	}
	var all map[string]string
	if err := json.Unmarshal(out, &all); err != nil {
		// Not the decoder's error: it can quote the input, which is secrets.
		return nil, fmt.Errorf("reading Doppler %s failed: the CLI's output was not the JSON it should be", scope)
	}
	return all, nil
}

// diagnostic is what a failed call says, from stderr only (the CLI writes its
// errors there and secrets to stdout), or the exit status when stderr is
// empty.
func diagnostic(stderr []byte, err error) string {
	if line := firstLine(string(stderr)); line != "" {
		return line
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Sprintf("doppler exited %d", exitErr.ExitCode())
	}
	return err.Error()
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
