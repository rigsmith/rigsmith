package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/files"
)

func TestConfigRestoreApplyRoundTrip(t *testing.T) {
	root := t.TempDir()
	putConfig(t, root, "config.toml", "model='old'\n[env]\nKEY='destination-private'")
	putConfig(t, root, "same.config.toml", "# unchanged comment\nmodel='same'")
	putConfig(t, root, "kept.config.toml", "# retained comment\nmodel='kept'")
	putConfig(t, root, "auth.json", "untouched-auth")
	backup := captureFiles(map[string]string{"config.toml": "model='new'", "new.config.toml": "model='fresh'", "same.config.toml": "model='same'"})
	plan, err := PrepareConfigRestore(t.Context(), Root{CodexHome, root}, backup, acceptConfig)
	if err != nil {
		t.Fatal(err)
	}
	result, err := plan.Apply(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Applied, []string{"config.toml", "new.config.toml"}) || result.Uncertain != "" {
		t.Fatalf("result: %#v", result)
	}
	data, err := os.ReadFile(filepath.Join(root, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := configDocument(t, data)
	if m["model"] != "new" || m["env"].(map[string]any)["KEY"] != "destination-private" {
		t.Fatal("lost local configuration")
	}
	for name, want := range map[string]string{"same.config.toml": "# unchanged comment\nmodel='same'", "kept.config.toml": "# retained comment\nmodel='kept'", "auth.json": "untouched-auth"} {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(got) != want {
			t.Fatal("unrelated/no-op file changed", name, err)
		}
	}
	if _, err := plan.Apply(t.Context()); !errors.Is(err, ErrConfigPlanClosed) {
		t.Fatal("plan applied twice", err)
	}
	if plan.proposed != nil {
		t.Fatal("private buffers retained")
	}
	// A fresh plan sees no changes and preserves every config byte.
	plan, err = PrepareConfigRestore(t.Context(), Root{CodexHome, root}, backup, acceptConfig)
	if err != nil {
		t.Fatal(err)
	}
	result, err = plan.Apply(t.Context())
	if err != nil || len(result.Applied) != 0 {
		t.Fatal("round-trip not idempotent", result, err)
	}
}

func TestConfigRestoreApplyNoopDoesNotCreateLock(t *testing.T) {
	root := t.TempDir()
	putConfig(t, root, "config.toml", "model='same'")
	p, err := PrepareConfigRestore(t.Context(), Root{CodexHome, root}, ConfigCapture{}, acceptConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatal("no-op mutated destination", err)
	}
}

func TestConfigRestoreApplyRefusesStaleAndBusyPlans(t *testing.T) {
	for _, scenario := range []string{"stale", "busy", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			putConfig(t, root, "config.toml", "model='old'")
			p, err := PrepareConfigRestore(t.Context(), Root{CodexHome, root}, captureFiles(map[string]string{"config.toml": "model='new'"}), acceptConfig)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := error(ErrConfigSourceChanged)
			switch scenario {
			case "stale":
				putConfig(t, root, "config.toml", "model='concurrent'")
			case "busy":
				holder, err := files.OpenSource(t.Context(), root)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Close()
				b, err := files.BeginReplace(t.Context(), holder)
				if err != nil {
					t.Fatal(err)
				}
				defer b.Close()
				want = files.ErrReplacementBusy
			case "cancel":
				cancel()
				want = context.Canceled
			}
			before, err := os.ReadFile(filepath.Join(root, "config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			result, err := p.Apply(ctx)
			if !errors.Is(err, want) || len(result.Applied) != 0 || result.Uncertain != "" {
				t.Fatal("bad refusal", result, err)
			}
			after, err := os.ReadFile(filepath.Join(root, "config.toml"))
			if err != nil || string(before) != string(after) {
				t.Fatal("refusal changed destination", err)
			}
		})
	}
}

type interruptedReplacements struct {
	configReplacements
	stages, applies        int
	stageErrAt, applyErrAt int
	afterApply             func()
	uncertain              bool
}

func (b *interruptedReplacements) Stage(ctx context.Context, name string, expected files.ExpectedFile, data []byte, limit int64) error {
	b.stages++
	if b.stages == b.stageErrAt {
		return files.ErrSource
	}
	return b.configReplacements.Stage(ctx, name, expected, data, limit)
}
func (b *interruptedReplacements) Apply(ctx context.Context, name string) error {
	b.applies++
	if b.applies == b.applyErrAt {
		if b.uncertain {
			return files.ErrReplacementUncertain
		}
		return files.ErrSourceChanged
	}
	if err := b.configReplacements.Apply(ctx, name); err != nil {
		return err
	}
	if b.afterApply != nil {
		b.afterApply()
	}
	return nil
}

func TestConfigRestoreApplyReportsPartialAndUncertainResults(t *testing.T) {
	for _, scenario := range []string{"late staging failure", "second refusal", "second uncertain", "edit retained profile"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			putConfig(t, root, "config.toml", "model='old'")
			putConfig(t, root, "kept.config.toml", "model='kept'")
			p, err := PrepareConfigRestore(t.Context(), Root{CodexHome, root}, captureFiles(map[string]string{"config.toml": "model='new'", "new.config.toml": "model='fresh'"}), acceptConfig)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			native, err := files.BeginReplace(t.Context(), p.source.(*files.Source))
			if err != nil {
				t.Fatal(err)
			}
			b := &interruptedReplacements{configReplacements: native}
			switch scenario {
			case "late staging failure":
				b.stageErrAt = 2
			case "second refusal":
				b.applyErrAt = 2
			case "second uncertain":
				b.applyErrAt = 2
				b.uncertain = true
			case "edit retained profile":
				b.afterApply = func() { putConfig(t, root, "kept.config.toml", "model='concurrent'") }
			}
			result, err := p.apply(t.Context(), b)
			if err == nil {
				t.Fatal("injected failure ignored")
			}
			if scenario == "late staging failure" {
				if len(result.Applied) != 0 || b.applies != 0 {
					t.Fatal("applied before every stage succeeded")
				}
				data, _ := os.ReadFile(filepath.Join(root, "config.toml"))
				if string(data) != "model='old'" {
					t.Fatal("staging damaged old config")
				}
			} else if !reflect.DeepEqual(result.Applied, []string{"config.toml"}) {
				t.Fatal("missing partial result", result)
			}
			if scenario == "second uncertain" && result.Uncertain != "new.config.toml" {
				t.Fatal("lost uncertain filename", result)
			}
			if _, err := os.Stat(filepath.Join(root, "new.config.toml")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unexpected later install")
			}
		})
	}
}
