package adapter

import (
	"context"
	"errors"
	"fmt"
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

func TestVersionedRestoreRejectsRuntimeRulesBeforeDestinationChecks(t *testing.T) {
	for _, scenario := range []string{"incoming collision", "retained missing transport", "mixed profile transport"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			original := "model='old'"
			backup := captureFiles(map[string]string{"config.toml": "model='new'"})
			switch scenario {
			case "incoming collision":
				backup = captureFiles(map[string]string{"config.toml": "[model_providers.openai]\nname='Collision'"})
			case "retained missing transport":
				putConfig(t, root, "retained.config.toml", "[mcp_servers.example]\nenabled=false")
			case "mixed profile transport":
				original += "\n[mcp_servers.example]\ncommand='private-local-helper'"
				putConfig(t, root, "retained.config.toml", "[mcp_servers.example]\nurl='https://example.com/mcp'")
			}
			putConfig(t, root, "config.toml", original)
			called := false
			p, err := PrepareVersionedConfigRestore(t.Context(), Root{CodexHome, root}, backup, configcodec.SupportedConfigVersion, func(context.Context, []ConfigFile) error {
				called = true
				return nil
			})
			if p != nil {
				p.Close()
				t.Fatal("invalid runtime configuration produced a plan")
			}
			if !errors.Is(err, ErrConfigValidation) || called {
				t.Fatal("runtime checks did not gate destination callback", err)
			}
			data, readErr := os.ReadFile(filepath.Join(root, "config.toml"))
			if readErr != nil || string(data) != original {
				t.Fatal("failed preparation changed destination", readErr)
			}
		})
	}
}

func TestLayeredRestoreUsesContextWithoutInstallingIt(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprint(invalid), func(t *testing.T) {
			root := t.TempDir()
			putConfig(t, root, "config.toml", "model='old'\ndefault_permissions='managed'")
			layers := configcodec.ValidationLayers{
				Before:       [][]byte{[]byte("[model_providers.local]\nname='Local'\nexperimental_bearer_token='private-context'")},
				Requirements: []byte("[permissions.managed]\nextends=':workspace'"),
			}
			if invalid {
				layers.Requirements = []byte("[permissions.managed]\nextends='missing'")
			}
			backup := captureFiles(map[string]string{"config.toml": "model='new'\nmodel_provider='local'"})
			called := false
			p, err := PrepareLayeredConfigRestore(t.Context(), Root{CodexHome, root}, backup, configcodec.SupportedConfigVersion, func(context.Context) (configcodec.ValidationLayers, error) { return layers, nil }, func(_ context.Context, proposed []ConfigFile) error {
				called = true
				if len(proposed) != 1 || strings.Contains(string(proposed[0].Data), "private-context") {
					t.Fatal("external context entered restore files")
				}
				return nil
			})
			if invalid {
				if p != nil {
					p.Close()
					t.Fatal("invalid context produced plan")
				}
				if called || !errors.Is(err, ErrConfigValidation) {
					t.Fatal("invalid context reached callback", err)
				}
				return
			}
			if err != nil || p == nil || !called {
				t.Fatal(err)
			}
			if _, err := p.Apply(t.Context()); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(root, "config.toml"))
			if err != nil || strings.Contains(string(data), "private-context") || strings.Contains(string(data), "extends") || !strings.Contains(string(data), "new") {
				t.Fatal("context was installed", err)
			}
			if _, err := os.Stat(filepath.Join(root, "requirements.toml")); !os.IsNotExist(err) {
				t.Fatal("requirements were installed", err)
			}
		})
	}
}

func TestLayeredRestoreRejectsChangedSourcesAtApply(t *testing.T) {
	for _, scenario := range []string{"before", "after", "requirements", "order", "arrival", "source error", "oversize", "no-op"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			putConfig(t, root, "config.toml", "model='old'")
			layers := configcodec.ValidationLayers{Before: [][]byte{[]byte("model='one'"), []byte("model='two'")}, After: [][]byte{[]byte("model_reasoning_effort='high'")}}
			var sourceErr error
			source := func(context.Context) (configcodec.ValidationLayers, error) { return layers, sourceErr }
			model := "new"
			if scenario == "no-op" {
				model = "old"
			}
			p, err := PrepareLayeredConfigRestore(t.Context(), Root{CodexHome, root}, captureFiles(map[string]string{"config.toml": "model='" + model + "'"}), configcodec.SupportedConfigVersion, source, acceptConfig)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "before", "no-op":
				layers.Before[0][7] = 'x'
			case "after":
				layers.After[0] = []byte("model_reasoning_effort='low'")
			case "requirements":
				layers.Requirements = []byte("default_permissions=':read-only'\n[allowed_permission_profiles]\n':read-only'=true")
			case "order":
				layers.Before[0], layers.Before[1] = layers.Before[1], layers.Before[0]
			case "arrival":
				layers.Requirements = []byte{}
			case "source error":
				sourceErr = errors.New("private-context-path")
			case "oversize":
				layers.After = make([][]byte, 17)
			}
			result, err := p.Apply(t.Context())
			if !errors.Is(err, ErrConfigLayersChanged) || len(result.Applied) != 0 || result.Uncertain != "" || strings.Contains(err.Error(), "private-context-path") {
				t.Fatal("stale layer accepted", result, err)
			}
			data, err := os.ReadFile(filepath.Join(root, "config.toml"))
			if err != nil || string(data) != "model='old'" {
				t.Fatal("stale context wrote config", err)
			}
			if p.checkContext != nil {
				t.Fatal("consumed plan retained layer reader")
			}
		})
	}
}

func TestLayeredRestoreRefusesContextChangeDuringValidation(t *testing.T) {
	root := t.TempDir()
	putConfig(t, root, "config.toml", "model='old'")
	layers := configcodec.ValidationLayers{}
	source := func(context.Context) (configcodec.ValidationLayers, error) { return layers, nil }
	p, err := PrepareLayeredConfigRestore(t.Context(), Root{CodexHome, root}, captureFiles(map[string]string{"config.toml": "model='new'"}), configcodec.SupportedConfigVersion, source, func(context.Context, []ConfigFile) error {
		layers.Requirements = []byte{}
		return nil
	})
	if p != nil {
		p.Close()
		t.Fatal("validation change produced plan")
	}
	if !errors.Is(err, ErrConfigValidation) {
		t.Fatal(err)
	}
	if p, err := PrepareLayeredConfigRestore(t.Context(), Root{CodexHome, root}, ConfigCapture{}, configcodec.SupportedConfigVersion, nil, acceptConfig); p != nil || !errors.Is(err, ErrConfigRestoreInput) {
		t.Fatal("nil source accepted", err)
	}
}
