package commands

import (
	"os"
	"testing"

	"github.com/rigsmith/clauderig/internal/config"
)

// machineName identifies the local machine by its stable path identity (OS token
// + home directory), not by picking an arbitrary map entry. With more than one
// registered machine it must resolve deterministically to the matching one,
// regardless of Go's randomized map iteration order.
func TestMachineName_DeterministicLocalMatch(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Machines: map[string]config.Machine{
			"other": {Name: "other", OS: "definitely-not-this-os", Home: "/nope/elsewhere"},
			"local": {Name: "local", OS: config.OSToken(), Home: home},
		},
	}

	// Loop to defeat map-iteration randomness: the result must be stable.
	for i := 0; i < 20; i++ {
		if got := machineName(cfg); got != "local" {
			t.Fatalf("iteration %d: machineName = %q, want %q", i, got, "local")
		}
	}
}

// With no registered machines, machineName falls back to a non-empty string
// (the OS hostname, or "this").
func TestMachineName_EmptyMachinesFallback(t *testing.T) {
	cfg := &config.Config{Machines: map[string]config.Machine{}}
	if got := machineName(cfg); got == "" {
		t.Fatalf("machineName returned empty string for empty Machines")
	}
}
