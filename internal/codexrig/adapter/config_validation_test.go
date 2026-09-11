package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/codexrig/configcodec"
)

func TestVersionedRestoreValidatesCompleteSetBeforeDestination(t *testing.T) {
	for _, scenario := range []string{"incoming invalid", "retained invalid", "profile independent", "valid", "destination refusal"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			putConfig(t, root, "config.toml", "model='old'\n[model_providers.custom]\nname='Custom'\nexperimental_bearer_token='private-local'")
			backup := captureFiles(map[string]string{"config.toml": "model='new'"})
			switch scenario {
			case "incoming invalid":
				backup = captureFiles(map[string]string{"config.toml": "model=123"})
			case "retained invalid":
				putConfig(t, root, "retained.config.toml", "model=false")
			case "profile independent":
				putConfig(t, root, "first.config.toml", "[model_providers.onlyhere]\nname='One'")
				putConfig(t, root, "second.config.toml", "model_provider='onlyhere'")
			case "valid", "destination refusal":
				putConfig(t, root, "retained.config.toml", "model_provider='custom'")
			}
			called := false
			p, err := PrepareVersionedConfigRestore(t.Context(), Root{CodexHome, root}, backup, configcodec.SupportedConfigVersion, func(_ context.Context, files []ConfigFile) error {
				called = true
				if len(files) != 2 || !strings.Contains(string(files[0].Data), "private-local") {
					t.Fatal("destination checks did not receive complete private proposed set")
				}
				if scenario == "destination refusal" {
					return errors.New("private-helper-path")
				}
				// Callback copies still cannot alter the eventual plan.
				files[0].Data[0] = '!'
				return nil
			})
			valid := scenario == "valid"
			if valid {
				if err != nil || p == nil || !called {
					t.Fatal(err)
				}
				result, err := p.Apply(t.Context())
				if err != nil || len(result.Applied) != 1 {
					t.Fatal(result, err)
				}
				data, err := os.ReadFile(filepath.Join(root, "config.toml"))
				if err != nil || !strings.Contains(string(data), "private-local") || !strings.Contains(string(data), "new") {
					t.Fatal("validated plan lost intended bytes", err)
				}
			} else {
				if p != nil {
					p.Close()
					t.Fatal("invalid set produced plan")
				}
				if !errors.Is(err, ErrConfigValidation) || strings.Contains(err.Error(), "private-helper-path") {
					t.Fatal("invalid refusal", err)
				}
				if called != (scenario == "destination refusal") {
					t.Fatal("destination callback bypassed schema validation")
				}
				data, _ := os.ReadFile(filepath.Join(root, "config.toml"))
				if !strings.Contains(string(data), "model='old'") {
					t.Fatal("validation wrote destination")
				}
			}
		})
	}
}

func TestVersionedRestoreRequiresSupportedVersionAndDestinationChecks(t *testing.T) {
	root := Root{CodexHome, filepath.Join(t.TempDir(), "absent")}
	if p, err := PrepareVersionedConfigRestore(t.Context(), root, ConfigCapture{}, "0.144.7", acceptConfig); p != nil || !errors.Is(err, configcodec.ErrUnsupportedVersion) {
		t.Fatal("version not checked before source", err)
	}
	if p, err := PrepareVersionedConfigRestore(t.Context(), root, ConfigCapture{}, configcodec.SupportedConfigVersion, nil); p != nil || !errors.Is(err, ErrConfigRestoreInput) {
		t.Fatal("destination validation optional", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if p, err := PrepareVersionedConfigRestore(ctx, root, ConfigCapture{}, configcodec.SupportedConfigVersion, acceptConfig); p != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
