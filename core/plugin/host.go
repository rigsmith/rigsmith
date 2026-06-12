package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Host runs an external plugin command, speaking the JSON-on-stdin /
// JSON-on-stdout protocol. One Host wraps one resolved command (path + base
// args); each Call invokes it afresh — plugins are stateless one-shots.
type Host struct {
	// Path is the executable to run.
	Path string
	// BaseArgs are prepended before the method name (e.g. ["run", "gen.js"] for
	// a `node gen.js <method>` plugin).
	BaseArgs []string
	// Dir is the working directory for the subprocess (usually the repo root).
	Dir string
}

// Call invokes `Path BaseArgs... method` with reqBody marshaled to stdin and
// decodes the stdout JSON into respBody (pass nil to ignore stdout). A non-zero
// exit is returned as an error carrying the plugin's stderr.
func (h *Host) Call(ctx context.Context, method string, reqBody any, respBody any) error {
	in, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("plugin %s: marshal request: %w", h.Path, err)
	}

	args := append(append([]string{}, h.BaseArgs...), method)
	cmd := exec.CommandContext(ctx, h.Path, args...)
	cmd.Dir = h.Dir
	cmd.Stdin = bytes.NewReader(in)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("plugin %s %s: %v: %s", h.Path, method, err, msg)
		}
		return fmt.Errorf("plugin %s %s: %w", h.Path, method, err)
	}

	if respBody == nil {
		return nil
	}
	if err := json.Unmarshal(stdout.Bytes(), respBody); err != nil {
		return fmt.Errorf("plugin %s %s: decode response: %w", h.Path, method, err)
	}
	return nil
}

// CallText invokes the plugin and returns its raw stdout (for the changelog
// contract, where stdout is rendered text, not JSON).
func (h *Host) CallText(ctx context.Context, method string, reqBody any) (string, error) {
	in, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("plugin %s: marshal request: %w", h.Path, err)
	}

	args := append(append([]string{}, h.BaseArgs...), method)
	cmd := exec.CommandContext(ctx, h.Path, args...)
	cmd.Dir = h.Dir
	cmd.Stdin = bytes.NewReader(in)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("plugin %s %s: %v: %s", h.Path, method, err, msg)
		}
		return "", fmt.Errorf("plugin %s %s: %w", h.Path, method, err)
	}
	return stdout.String(), nil
}
