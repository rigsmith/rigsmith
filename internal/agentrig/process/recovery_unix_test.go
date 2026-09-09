//go:build linux || darwin

package process

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

func TestRecoveryScopeAndPrelaunchEvidence(t *testing.T) {
	for _, mode := range []string{"prepared", "host", "namespace", "reboot", "version", "platform", "group"} {
		t.Run(mode, func(t *testing.T) {
			evidence, err := newEvidence()
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "host":
				evidence.Scope.Host = digest("other host")
			case "namespace":
				evidence.Scope.Namespace = digest("other namespace")
			case "reboot":
				evidence.Scope.Boot = digest("synthetic previous boot")
			case "version":
				evidence.Version++
			case "platform":
				evidence.Platform = "other"
			case "group":
				evidence.Group = -1
			}
			root := t.TempDir()
			stage := filepath.Join(root, "store")
			if err := os.Mkdir(stage, 0700); err != nil {
				t.Fatal(err)
			}
			payload := filepath.Join(stage, "saved-capture")
			if err := os.WriteFile(payload, []byte("retained bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, release, err := storelock.Acquire(t.Context(), stage, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := storelock.BeginRecoverableFence(ctx, evidence.bytes()); err != nil {
				t.Fatal(err)
			}
			if recovered, err := RecoverStore(t.Context(), stage); recovered || !errors.Is(err, storelock.ErrBusy) {
				t.Fatalf("recovered active lease: %v %v", recovered, err)
			}
			release()
			recovered, err := RecoverStore(t.Context(), stage)
			want := mode == "prepared" || mode == "reboot"
			if recovered != want || (err == nil) != want {
				t.Fatalf("recovery(%s) = %v %v", mode, recovered, err)
			}
			data, err := os.ReadFile(payload)
			if err != nil || string(data) != "retained bytes" {
				t.Fatalf("recovery changed saved capture: %q %v", data, err)
			}
		})
	}
}
