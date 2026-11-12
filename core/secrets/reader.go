package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Reader reads named secrets for one purpose (release, typically): from the
// provider's CLI on a developer's machine, and from the environment in CI,
// where the workflow's fetch step passed each secret to the step that needs
// it. Either way a missing or empty secret is an error.
type Reader struct {
	setup   *Setup
	purpose Context
	dir     string
	// getenv reads the job's environment in CI; a field so tests can supply one.
	getenv func(string) string
}

// NewReader returns a Reader for setup's config for purpose, running the CLI
// in dir. A nil setup yields nil: no provider is configured.
func NewReader(setup *Setup, purpose Context, dir string) *Reader {
	if setup == nil {
		return nil
	}
	return &Reader{setup: setup, purpose: purpose, dir: dir, getenv: os.Getenv}
}

// Provider is the provider the Reader reads from.
func (r *Reader) Provider() Provider { return r.setup.Provider }

// Ready checks the provider's CLI in the Reader's folder.
func (r *Reader) Ready(ctx context.Context) error { return r.setup.Provider.Ready(ctx, r.dir) }

// Available reports whether Get has somewhere to read from: the job's
// environment in CI, or a config for the Reader's purpose locally. A secret
// reference with a fallback uses the fallback when this is false — a repo that
// has not set a release config yet keeps its keychain.
func (r *Reader) Available() bool {
	if r == nil {
		return false
	}
	if InCI() {
		return true
	}
	_, ok := r.setup.Scope(r.purpose)
	return ok
}

// Get reads one secret and says where it came from, by name only ("Doppler
// foxcraft/release", "the job environment"). The value is never in an error.
func (r *Reader) Get(ctx context.Context, name string) (value, source string, err error) {
	p := r.setup.Provider
	if !ValidName(name) {
		return "", "", fmt.Errorf("%q is not a secret name (letters, digits and '_')", name)
	}
	if InCI() {
		v := strings.TrimSpace(r.getenv(name))
		if v == "" {
			return "", "", fmt.Errorf("%s is not set in this CI step — pass it from the %s fetch step "+
				"(`env: %s: ${{ steps.%s.outputs.%s }}`; `rig secrets ci` writes that step)",
				name, p.Title(), name, DefaultStepID, name)
		}
		return v, "the job environment (" + name + ")", nil
	}
	scope, ok := r.setup.Scope(r.purpose)
	if !ok {
		return "", "", fmt.Errorf("%s:%s needs %s.%s in .rig.json — the %s config to read it from",
			p.Name(), name, p.Name(), r.purpose, r.purpose)
	}
	if err := p.Ready(ctx, r.dir); err != nil {
		return "", "", err
	}
	v, err := p.Get(ctx, r.dir, scope, name)
	if err != nil {
		return "", "", err
	}
	return v, p.Title() + " " + scope.String(), nil
}

// Describe names where Get reads name from, without reading it: the job
// environment in CI, the provider's config for the Reader's purpose locally.
func (r *Reader) Describe(name string) string {
	if InCI() {
		return "the job environment (" + name + ")"
	}
	if scope, ok := r.setup.Scope(r.purpose); ok {
		return r.setup.Provider.Title() + " " + scope.String() + " (" + name + ")"
	}
	return r.setup.Provider.Name() + ":" + name + " (no " + string(r.purpose) + " config)"
}

// ParseRef splits a provider secret reference ("doppler:NAME") into its
// provider key and secret name. False for anything else — an op://, env: or
// cmd: reference, or a prefix no provider claims.
func ParseRef(ref string) (provider, name string, ok bool) {
	key, rest, found := strings.Cut(strings.TrimSpace(ref), ":")
	if !found || strings.HasPrefix(rest, "//") {
		return "", "", false
	}
	if _, known := providers[strings.ToLower(key)]; !known {
		return "", "", false
	}
	return strings.ToLower(key), strings.TrimSpace(rest), true
}

type readerKey struct{}

// WithReader returns ctx carrying r, so a reference resolved deep in a
// publish or a signing step reads from the repo's provider without every
// caller threading it through.
func WithReader(ctx context.Context, r *Reader) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, readerKey{}, r)
}

// ReaderFrom returns the Reader ctx carries, or nil.
func ReaderFrom(ctx context.Context) *Reader {
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value(readerKey{}).(*Reader)
	return r
}
