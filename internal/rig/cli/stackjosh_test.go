package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// The engine's own git is a separate process, and the credential reaches it
// through the environment alone — so what the command carries is the whole
// handoff, and the one place a private import can silently lose its token.
func TestJoshProxyCommandHandsTheCredentialToTheEngine(t *testing.T) {
	ctx := context.Background()
	t.Setenv("GIT_CONFIG_COUNT", "")

	t.Run("no credential leaves the environment alone", func(t *testing.T) {
		cmd := joshProxyCommand(ctx, "josh-proxy", "github.com", "/cache", 8080, nil)
		if cmd.Env != nil {
			t.Fatalf("a nil credential should not touch the environment, got %d entries", len(cmd.Env))
		}
	})

	t.Run("a credential arrives scoped to the forge, and never on argv", func(t *testing.T) {
		auth := &gitrepo.HTTPAuth{Username: "x-access-token", Password: "gho_secret", URLPrefix: "https://github.com/"}
		cmd := joshProxyCommand(ctx, "josh-proxy", "github.com", "/cache", 8080, auth)
		env := strings.Join(cmd.Env, "\n")
		for _, want := range []string{
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.https://github.com/.extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic ",
		} {
			if !strings.Contains(env, want) {
				t.Errorf("engine environment lacks %q:\n%s", want, env)
			}
		}
		if strings.Contains(env, "gho_secret") {
			t.Error("the token is in the environment in the clear rather than encoded")
		}
		if argv := strings.Join(cmd.Args, " "); strings.Contains(argv, "gho_secret") || strings.Contains(argv, "Authorization") {
			t.Errorf("the credential reached argv, which any process on the machine can read: %s", argv)
		}
		if !strings.Contains(strings.Join(cmd.Args, " "), "--remote https://github.com") {
			t.Errorf("the remote is not the forge the credential is scoped to: %v", cmd.Args)
		}
	})
}
