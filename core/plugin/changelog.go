package plugin

import (
	"context"
	"strings"
)

// ChangelogGenerator renders a single release entry from a ChangelogRequest.
// The built-in generator implements this in-process; SubprocessChangelogGenerator
// delegates to an external command — both behind the same interface, so the
// built-in is the reference implementation of the contract (dogfooding).
type ChangelogGenerator interface {
	// ID is the generator's identifier ("default", or a plugin name).
	ID() string
	// Render returns the rendered release entry (the block under the package's
	// "# Title", excluding the title). The engine owns file placement/insertion.
	Render(ctx context.Context, req ChangelogRequest) (string, error)
}

// SubprocessChangelogGenerator delegates rendering to an external command per
// the changelog plugin contract: ChangelogRequest JSON on stdin, rendered entry
// text on stdout.
type SubprocessChangelogGenerator struct {
	id   string
	host *Host
}

// NewSubprocessChangelogGenerator wires an external generator command.
func NewSubprocessChangelogGenerator(id string, host *Host) *SubprocessChangelogGenerator {
	return &SubprocessChangelogGenerator{id: id, host: host}
}

func (g *SubprocessChangelogGenerator) ID() string { return g.id }

func (g *SubprocessChangelogGenerator) Render(ctx context.Context, req ChangelogRequest) (string, error) {
	req.APIVersion = APIVersion
	out, err := g.host.CallText(ctx, "render", req)
	if err != nil {
		return "", err
	}
	// The engine trims a single trailing newline and owns insertion position.
	return strings.TrimRight(out, "\n"), nil
}

var _ ChangelogGenerator = (*SubprocessChangelogGenerator)(nil)

// ResolveChangelogGenerator turns a config value into a generator.
//
//	"default" (or any registered built-in id) -> the in-process built-in
//	a path (contains '/' or '\', or starts with '.') -> executed directly
//	a bare name -> looked up on $PATH by the changeset-changelog-* convention
//
// builtins maps built-in ids to their implementations (so the host app injects
// its BuiltinChangelogGenerator without core depending on changelog rendering).
func ResolveChangelogGenerator(spec string, dir string, builtins map[string]ChangelogGenerator) (ChangelogGenerator, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		spec = "default"
	}
	if g, ok := builtins[spec]; ok {
		return g, nil
	}
	if isPath(spec) {
		return NewSubprocessChangelogGenerator(spec, &Host{Path: spec, Dir: dir}), nil
	}
	// Bare name: resolve `changeset-changelog-<name>` on PATH (git-foo convention).
	return NewSubprocessChangelogGenerator(spec, &Host{Path: "changeset-changelog-" + spec, Dir: dir}), nil
}

func isPath(s string) bool {
	return strings.ContainsAny(s, "/\\") || strings.HasPrefix(s, ".")
}
