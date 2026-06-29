package plugin

import (
	"os"
	"testing"
)

// TestArtifactsRequestBaseEnv pins that BaseEnv prefers the engine-provided
// release env (so a build subprocess inherits .env/.env.local) and falls back to
// the process environment when none was provided.
func TestArtifactsRequestBaseEnv(t *testing.T) {
	want := []string{"AZURE_CLIENT_SECRET=shh", "FOO=bar"}
	if got := (ArtifactsRequest{Env: want}).BaseEnv(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("BaseEnv with Env set = %v, want %v", got, want)
	}

	// No Env → the adapter process environment (os.Environ is never empty in a
	// real process; assert it falls through to exactly that).
	fallback := (ArtifactsRequest{}).BaseEnv()
	if len(fallback) != len(os.Environ()) {
		t.Errorf("BaseEnv with no Env returned %d entries, want os.Environ() (%d)", len(fallback), len(os.Environ()))
	}
}
