package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/rigsmith/rigsmith/internal/agentrig/files"
	"github.com/rigsmith/rigsmith/internal/codexrig/configcodec"
)

func acceptConfig(context.Context, []ConfigFile) error { return nil }

func captureFiles(entries map[string]string) ConfigCapture {
	out := ConfigCapture{}
	for name, body := range entries {
		out.Files = append(out.Files, ConfigFile{Path: name, Data: []byte(body)})
	}
	return out
}

func configDocument(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := toml.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPrepareConfigRestoreCompletePrivatePlan(t *testing.T) {
	root := t.TempDir()
	local := map[string]string{
		"config.toml":          "# local formatting\nmodel = 'old'\n[env]\nSHORT = 'destination-private'",
		"same.config.toml":     "# preserve this comment\nmodel='same'\n",
		"retained.config.toml": "# not in backup\nmodel='retained'\n",
		"auth.json":            "private authentication bytes that must not be opened",
	}
	for name, body := range local {
		putConfig(t, root, name, body)
	}
	backup := captureFiles(map[string]string{"config.toml": "model = 'new'", "same.config.toml": "model='same'", "new.config.toml": "model='fresh'"})
	var validated []ConfigFile
	plan, err := PrepareConfigRestore(t.Context(), Root{CodexHome, root}, backup, func(ctx context.Context, proposed []ConfigFile) error {
		validated = proposed
		if len(proposed) != 4 {
			t.Fatalf("validator missed local or new profiles: %#v", proposed)
		}
		base := configDocument(t, proposed[0].Data)
		if base["model"] != "new" || base["env"].(map[string]any)["SHORT"] != "destination-private" {
			t.Fatal("validator did not receive the actual merged destination config")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	want := []ConfigRestoreChange{{"config.toml", "update"}, {"new.config.toml", "create"}, {"same.config.toml", "unchanged"}}
	if !reflect.DeepEqual(plan.Changes(), want) {
		t.Fatalf("changes = %#v", plan.Changes())
	}
	for _, f := range plan.proposed {
		if f.Path == "same.config.toml" || f.Path == "retained.config.toml" {
			if string(f.Data) != local[f.Path] {
				t.Fatal("no-op or retained profile lost original formatting")
			}
		}
	}
	// Neither callers nor validators can alter an already validated plan.
	before := bytes.Clone(plan.proposed[0].Data)
	for _, file := range backup.Files {
		clear(file.Data)
	}
	clear(validated[0].Data)
	validated[0].Path = "changed"
	changes := plan.Changes()
	changes[0].Action = "changed"
	if !bytes.Equal(plan.proposed[0].Data, before) || !reflect.DeepEqual(plan.Changes(), want) {
		t.Fatal("external mutation changed plan")
	}
	for _, representation := range []string{fmt.Sprint(plan), fmt.Sprintf("%+v", plan), fmt.Sprintf("%#v", plan), fmt.Sprintf("%#v", *plan)} {
		if strings.Contains(representation, "destination-private") || strings.Contains(representation, root) || strings.Contains(representation, "model") {
			t.Fatal("formatted plan exposed private state")
		}
	}
	encoded, err := json.Marshal(plan)
	if err != nil || string(encoded) != "{}" {
		t.Fatalf("plan serialized: %s %v", encoded, err)
	}
	for name, body := range local {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(got) != body {
			t.Fatalf("preparation changed a file: %s %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "new.config.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preparation created a file")
	}
	if err := plan.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	if plan.proposed != nil || plan.original != nil || plan.Changes() != nil || plan.source != nil {
		t.Fatal("closed plan retained private state")
	}
	if !errors.Is(plan.Check(t.Context()), ErrConfigPlanClosed) || plan.Close() != nil {
		t.Fatal("closed plan accepted a check or failed a second close")
	}
}

func TestPrepareConfigRestoreRejectsInvalidInput(t *testing.T) {
	for _, name := range []string{"auth.json", "../config.toml", "nested/config.toml", "/config.toml", "bad.name.config.toml", "CON.config.toml", "CONFIG.TOML"} {
		plan, err := PrepareConfigRestore(t.Context(), Root{CodexHome, t.TempDir()}, captureFiles(map[string]string{name: "model='safe'"}), acceptConfig)
		if err == nil || plan != nil {
			t.Fatalf("accepted backup name %q", name)
		}
	}
	for _, names := range [][]string{{"config.toml", "config.toml"}, {"work.config.toml", "WORK.config.toml"}} {
		backup := ConfigCapture{Files: []ConfigFile{{Path: names[0]}, {Path: names[1]}}}
		if plan, err := PrepareConfigRestore(t.Context(), Root{CodexHome, t.TempDir()}, backup, acceptConfig); !errors.Is(err, ErrConfigNames) || plan != nil {
			t.Fatalf("accepted duplicate names: %v", err)
		}
	}
	for _, body := range []string{"broken = [", "password='source-private'", "unknown='/source/path'"} {
		root := t.TempDir()
		putConfig(t, root, "config.toml", "model='old'")
		backup := captureFiles(map[string]string{"config.toml": "model='new'", "z.config.toml": body})
		called := false
		plan, err := PrepareConfigRestore(t.Context(), Root{CodexHome, root}, backup, func(context.Context, []ConfigFile) error { called = true; return nil })
		if err == nil || plan != nil || called {
			t.Fatalf("bad late profile produced a plan or reached validator: %v", err)
		}
	}
	if p, err := PrepareConfigRestore(t.Context(), Root{CodexHome, t.TempDir()}, ConfigCapture{}, nil); !errors.Is(err, ErrConfigRestoreInput) || p != nil {
		t.Fatal("missing validator accepted")
	}
	if p, err := PrepareConfigRestore(t.Context(), Root{UserSkills, t.TempDir()}, ConfigCapture{}, acceptConfig); !errors.Is(err, ErrConfigRestoreInput) || p != nil {
		t.Fatal("wrong destination kind accepted")
	}
	missing := filepath.Join(t.TempDir(), "absent")
	if p, err := PrepareConfigRestore(t.Context(), Root{CodexHome, missing}, ConfigCapture{}, acceptConfig); !errors.Is(err, os.ErrNotExist) || p != nil {
		t.Fatalf("missing home accepted: %v", err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing home created")
	}
}

func TestPrepareConfigRestoreValidationRefusalAndLocalSyntax(t *testing.T) {
	root := t.TempDir()
	putConfig(t, root, "config.toml", "model='old'")
	backup := captureFiles(map[string]string{"config.toml": "model='new'"})
	p, err := PrepareConfigRestore(t.Context(), Root{CodexHome, root}, backup, func(context.Context, []ConfigFile) error { return errors.New("destination-private") })
	if p != nil || !errors.Is(err, ErrConfigValidation) || strings.Contains(err.Error(), "destination-private") {
		t.Fatalf("validator refusal leaked or produced plan: %v", err)
	}
	putConfig(t, root, "untouched.config.toml", "broken = [")
	p, err = PrepareConfigRestore(t.Context(), Root{CodexHome, root}, backup, acceptConfig)
	if p != nil || !errors.Is(err, configcodec.ErrSyntax) {
		t.Fatalf("malformed retained profile accepted: %v", err)
	}
}

func TestPrepareConfigRestoreDestinationChanges(t *testing.T) {
	for _, mutation := range []string{"edit", "new profile", "remove profile", "cancel", "unrelated file"} {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			putConfig(t, root, "config.toml", "model='old'")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			p, err := PrepareConfigRestore(ctx, Root{CodexHome, root}, captureFiles(map[string]string{"config.toml": "model='new'"}), func(context.Context, []ConfigFile) error {
				switch mutation {
				case "edit":
					path := filepath.Join(root, "config.toml")
					st, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					putConfig(t, root, "config.toml", "model='NEW'")
					if err := os.Chtimes(path, st.ModTime(), st.ModTime()); err != nil {
						t.Fatal(err)
					}
				case "new profile":
					putConfig(t, root, "new.config.toml", "model='other'")
				case "remove profile":
					if err := os.Remove(filepath.Join(root, "config.toml")); err != nil {
						t.Fatal(err)
					}
				case "cancel":
					cancel()
				case "unrelated file":
					putConfig(t, root, "history.jsonl", "unrelated")
				}
				return nil
			})
			if mutation == "unrelated file" {
				if err != nil {
					t.Fatal(err)
				}
				p.Close()
				return
			}
			want := ErrConfigSourceChanged
			if mutation == "cancel" {
				want = context.Canceled
			}
			if p != nil || !errors.Is(err, want) {
				t.Fatalf("change escaped validation: %v", err)
			}
		})
	}
}

func TestConfigRestorePlanDetectsLaterChangesAndRootReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	putConfig(t, root, "config.toml", "model='old'")
	p, err := PrepareConfigRestore(t.Context(), Root{CodexHome, root}, ConfigCapture{}, acceptConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	putConfig(t, root, "config.toml", "model='changed'")
	if !errors.Is(p.Check(t.Context()), ErrConfigSourceChanged) {
		t.Fatal("later edit accepted")
	}
	putConfig(t, root, "config.toml", "model='old'")
	if err := os.Rename(root, root+"-moved"); err != nil {
		t.Fatal(err)
	}
	putConfig(t, root, "config.toml", "model='old'")
	if !errors.Is(p.Check(t.Context()), ErrConfigSourceChanged) {
		t.Fatal("replacement root accepted")
	}
}

func TestConfigRestoreNamesLinksAndLimits(t *testing.T) {
	for _, name := range []string{"CONFIG.TOML", "work.CONFIG.TOML", "WORK.config.toml"} {
		root := t.TempDir()
		putConfig(t, root, name, "model='local'")
		p, err := PrepareConfigRestore(t.Context(), Root{CodexHome, root}, captureFiles(map[string]string{"work.config.toml": "model='remote'"}), acceptConfig)
		if !errors.Is(err, ErrConfigNames) || p != nil {
			t.Fatalf("case alias accepted: %v", err)
		}
	}
	t.Run("directory", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "config.toml"), 0o700); err != nil {
			t.Fatal(err)
		}
		if p, err := PrepareConfigRestore(t.Context(), Root{CodexHome, root}, ConfigCapture{}, acceptConfig); !errors.Is(err, files.ErrSource) || p != nil {
			t.Fatalf("directory accepted: %v", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside.toml")
		putConfig(t, filepath.Dir(outside), filepath.Base(outside), "model='private'")
		if err := os.Symlink(outside, filepath.Join(root, "config.toml")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if p, err := PrepareConfigRestore(t.Context(), Root{CodexHome, root}, ConfigCapture{}, acceptConfig); !errors.Is(err, files.ErrSource) || p != nil {
			t.Fatalf("link accepted: %v", err)
		}
	})
	t.Run("result file count", func(t *testing.T) {
		root := t.TempDir()
		for i := 0; i < MaxConfigFiles; i++ {
			putConfig(t, root, fmt.Sprintf("p%d.config.toml", i), "")
		}
		if p, err := PrepareConfigRestore(t.Context(), Root{CodexHome, root}, captureFiles(map[string]string{"new.config.toml": ""}), acceptConfig); !errors.Is(err, files.ErrSourceLimit) || p != nil {
			t.Fatalf("oversized combined set accepted: %v", err)
		}
	})
	t.Run("bytes", func(t *testing.T) {
		root := t.TempDir()
		backup := ConfigCapture{}
		for i := 0; i < 9; i++ {
			backup.Files = append(backup.Files, ConfigFile{Path: fmt.Sprintf("p%d.config.toml", i), Data: bytes.Repeat([]byte(" "), configcodec.MaxBytes)})
		}
		if p, err := PrepareConfigRestore(t.Context(), Root{CodexHome, root}, backup, acceptConfig); !errors.Is(err, files.ErrSourceLimit) || p != nil {
			t.Fatalf("oversized backup accepted: %v", err)
		}
		for _, f := range backup.Files {
			putConfig(t, root, f.Path, string(f.Data))
		}
		if p, err := PrepareConfigRestore(t.Context(), Root{CodexHome, root}, ConfigCapture{}, acceptConfig); !errors.Is(err, files.ErrSourceLimit) || p != nil {
			t.Fatalf("oversized local set accepted: %v", err)
		}
	})
}

func TestRestorePlanRechecksNamesAfterReading(t *testing.T) {
	root := t.TempDir()
	putConfig(t, root, "config.toml", "model='old'")
	source, err := files.OpenSource(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	changing := &changingSource{Source: source, afterRead: func(n int) {
		if n == 2 { // During the pre-validation check, after its first listing.
			putConfig(t, root, "late.config.toml", "model='late'")
		}
	}}
	called := false
	p, err := prepareConfigRestore(t.Context(), changing, nil, func(context.Context, []ConfigFile) error { called = true; return nil })
	if p != nil || !errors.Is(err, ErrConfigSourceChanged) || called {
		t.Fatalf("arrival during check missed: %v, validator called: %v", err, called)
	}
}

func TestRestorePlanMergedBytesLimit(t *testing.T) {
	root := t.TempDir()
	backup := ConfigCapture{}
	// Each side is below 8 MiB and each merged file below 1 MiB, but the
	// complete merged set exceeds 8 MiB. Refuse before calling the validator.
	body := strings.Repeat("public ", 38000)
	for i := 0; i < 16; i++ {
		name := fmt.Sprintf("p%d.config.toml", i)
		putConfig(t, root, name, "local = '"+body+"'")
		backup.Files = append(backup.Files, ConfigFile{Path: name, Data: []byte("remote = '" + body + "'")})
	}
	called := false
	p, err := PrepareConfigRestore(t.Context(), Root{CodexHome, root}, backup, func(context.Context, []ConfigFile) error { called = true; return nil })
	if p != nil || !errors.Is(err, files.ErrSourceLimit) || called {
		t.Fatalf("oversized merged set accepted: %v", err)
	}
}
