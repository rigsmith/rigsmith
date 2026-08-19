package commands

import (
	"strings"
	"testing"
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

func TestSessionProfileEnvPrefersTheStorageVar(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/config-dir")
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "/storage-dir")
	// Claude Code resolves its credential store through the securestorage var
	// first, so that is the one that decides which credential `add` would read.
	if got := sessionProfileEnv(); got != "/storage-dir" {
		t.Fatalf("sessionProfileEnv() = %q, want /storage-dir", got)
	}
}

func TestSessionProfileEnvIgnoresBlankValues(t *testing.T) {
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "   ")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if got := sessionProfileEnv(); got != "" {
		t.Fatalf("sessionProfileEnv() = %q, want empty — a blank var is not a profile", got)
	}
}
