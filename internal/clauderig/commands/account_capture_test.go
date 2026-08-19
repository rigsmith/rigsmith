package commands

import (
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/account"
)

// `add` reads the machine-wide login. Run from a session terminal, that is a
// different account than the one the operator is looking at — so it must refuse
// rather than file the wrong credential under the profile block's name.
func TestCaptureCurrentRefusesInsideASessionProfile(t *testing.T) {
	for _, env := range []string{"CLAUDE_CONFIG_DIR", "CLAUDE_SECURESTORAGE_CONFIG_DIR"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, t.TempDir())
			// Refused before the store is touched, so a nil store is never used.
			_, _, err := captureCurrent(nil)
			if err == nil {
				t.Fatal("captured the machine-wide credential from inside a session profile")
			}
			if !strings.Contains(err.Error(), "isolated profile") {
				t.Fatalf("error does not explain the refusal: %v", err)
			}
			if !strings.Contains(err.Error(), "--from-session") {
				t.Fatalf("error does not point at the repair path: %v", err)
			}
		})
	}
}

// The two variables select independent surfaces, so a divergence in EITHER is
// enough to make `add` capture a mismatched pair — one profile's credential
// filed under another's identity.
func TestIsolatedProfileDirCatchesEitherVariable(t *testing.T) {
	home, err := account.ClaudeHome()
	if err != nil {
		t.Skip("no resolvable claude home")
	}
	t.Run("storage diverges, config is default", func(t *testing.T) {
		t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", t.TempDir())
		t.Setenv("CLAUDE_CONFIG_DIR", home)
		if got := isolatedProfileDir(); got == "" {
			t.Fatal("a diverging credential store was not caught")
		}
	})
	t.Run("config diverges, storage is default", func(t *testing.T) {
		t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", home)
		t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
		if got := isolatedProfileDir(); got == "" {
			t.Fatal("a diverging identity profile was not caught")
		}
	})
	t.Run("both name the default profile", func(t *testing.T) {
		t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", home)
		t.Setenv("CLAUDE_CONFIG_DIR", home)
		if got := isolatedProfileDir(); got != "" {
			t.Fatalf("isolatedProfileDir() = %q, want empty — both name ~/.claude", got)
		}
	})
}

func TestIsolatedProfileDirIgnoresBlankValues(t *testing.T) {
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "   ")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if got := isolatedProfileDir(); got != "" {
		t.Fatalf("isolatedProfileDir() = %q, want empty — a blank var is not a profile", got)
	}
}
