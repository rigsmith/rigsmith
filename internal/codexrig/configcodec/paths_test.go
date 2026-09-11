package configcodec

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestCaptureKeepsMachineSettingsLocal(t *testing.T) {
	// Known fields stay local even with relative paths, special permission
	// tokens, or malformed types. We do not reinterpret them on a new machine.
	for _, field := range []string{
		"permissions", "default_permissions", "sandbox_mode", "sandbox_workspace_write",
		"model_instructions_file", "experimental_compact_prompt_file", "model_catalog_json",
		"otel", "marketplaces", "plugins", "skills.config", "desktop.custom_file_handlers",
		"features.network_proxy", "shell_environment_policy.set", "agents.reviewer.config_file", "mcp_servers.example.cwd",
	} {
		for _, value := range []string{`'relative-name'`, `'/source/path'`, `1234`, `true`, `['../source', 'C:\source']`, `{ '/source/path' = 'write' }`} {
			t.Run(field+"/"+value, func(t *testing.T) {
				source := []byte("model = 'portable'\n" + field + " = " + value)
				backup, err := Capture(source)
				if err != nil {
					t.Fatal(err)
				}
				node := document(t, backup)
				parts := strings.Split(field, ".")
				for _, key := range parts[:len(parts)-1] {
					node = node[key].(map[string]any)
				}
				if _, exists := node[parts[len(parts)-1]]; exists {
					t.Fatalf("machine setting copied: %s", backup)
				}
				if got, err := Restore(source, nil); !errors.Is(err, ErrUnsafeBackup) || got != nil {
					t.Fatalf("unsanitized backup accepted: %s %v", got, err)
				}
				local := []byte("model = 'old'\n" + field + " = 'destination-local'")
				got, err := Restore(backup, local)
				if err != nil {
					t.Fatal(err)
				}
				want := document(t, local)
				want["model"] = "portable"
				if !reflect.DeepEqual(document(t, got), want) {
					t.Fatalf("local field not preserved: %s", got)
				}
				fresh, err := Restore(backup, nil)
				if err != nil || !bytes.Equal(fresh, backup) {
					t.Fatalf("fresh restore fabricated local values: %s %v", fresh, err)
				}
			})
		}
	}
}

func TestUnclassifiedLocalReferencesRefuseCaptureAndRestore(t *testing.T) {
	for _, value := range []string{
		"/Users/source/repo", "/", "/etc/config", "C:\\Users\\source", "c:/Users/source", "Z:relative",
		`\\server\share\config`, `\rooted`, `\\?\C:\config`, `\\.\pipe\config`, "//server/share",
		"~/config", "~source/config", "~", "./config", "../config", `.\config`, `..\config`,
		"$HOME/config", "${CODEX_HOME}/config", "%USERPROFILE%\\config", "$env:USERPROFILE", "${ROOT}",
		"file:///tmp/config", "FILE://host/share", "file:/tmp/config", "file:relative",
		"read /source/path please", "location=(C:\\source)", "read `~/config`", "location=/tmp/config", "paths: [/tmp/a, /tmp/b]",
	} {
		t.Run(value, func(t *testing.T) {
			for _, node := range []map[string]any{
				{"unknown": value}, {"unknown": []any{"public", value}}, {"unknown": map[string]any{value: true}},
			} {
				source, err := toml.Marshal(node)
				if err != nil {
					t.Fatal(err)
				}
				for _, operation := range []func([]byte) ([]byte, error){Capture, func(b []byte) ([]byte, error) { return Restore(b, nil) }} {
					got, err := operation(source)
					if !errors.Is(err, ErrUnclassifiedLocalReference) || got != nil {
						t.Fatalf("unclassified path accepted: %s %v", got, err)
					}
					if err.Error() != ErrUnclassifiedLocalReference.Error() {
						t.Fatalf("source text in diagnostic: %v", err)
					}
				}
			}
		})
	}
	// TOML escaping is decoded before policy is applied.
	for _, source := range []string{`unknown = "\u002fsource/path"`, `"\u002fsource/path" = true`, `unknown = "\u0024HOME"`} {
		if got, err := Capture([]byte(source)); !errors.Is(err, ErrUnclassifiedLocalReference) || got != nil {
			t.Fatalf("escaped path accepted: %s %v", got, err)
		}
	}
}

func TestPublicValuesAndScopedPathNamesRemainPortable(t *testing.T) {
	source := []byte(`model = "org/example-model"
project_doc_fallback_filenames = ["README.md", "NOTES.md"]
project_root_markers = [".git"]
[unknown]
path = "repo/label"
config_file = "name"
permissions = "label"
input = "path"
url = "https://example.com/v1/models"
text = "See https://example.com/reference or use a/b for a ratio."
[skills]
max_context_tokens = 1000
[agents]
enabled = true
max_threads = 2
[agents.reviewer]
description = "Check correctness"
[mcp_servers.example]
url = "https://example.com/mcp"
env_vars = ["HOME", "USERPROFILE"]
env_http_headers = { Authorization = "EXAMPLE_AUTH" }
[model_providers.example]
base_url = "https://example.com/v1"
env_key = "EXAMPLE_API_KEY"
`)
	backup, err := Capture(source)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document(t, backup), document(t, source)) {
		t.Fatalf("public value altered: %s", backup)
	}
	for _, value := range []string{"https://example.com/?code=short", "https://user:short@example.com/path"} {
		if _, err := Capture([]byte("value = '" + value + "'")); !errors.Is(err, ErrSecret) {
			t.Fatalf("URL path exemption bypassed credentials: %v", err)
		}
	}
}

func TestRestorePreservesLocalPathIdentity(t *testing.T) {
	for _, section := range []string{"agents", "mcp_servers", "model_providers"} {
		for _, field := range []string{"unknown = '/destination/path'", "cwd = '/destination/path'", "config_file = 'relative.toml'"} {
			// Only agents.config_file is a documented relative path in this table.
			if field == "config_file = 'relative.toml'" && section != "agents" {
				continue
			}
			local := []byte("[" + section + ".example]\nname = 'local'\n" + field)
			backup := []byte("[" + section + ".example]\nname = 'remote'\nurl = 'https://other.example'")
			got, err := Restore(backup, local)
			if err != nil || !reflect.DeepEqual(document(t, got), document(t, local)) {
				t.Fatalf("local identity rebound: %s %v", got, err)
			}
		}
	}
	for _, local := range []string{
		`unknown = '/destination/path'`,
		`unknown = [{ name = 'first', value = '/destination/path' }, { name = 'second' }]`,
		`unknown = { '/destination/path' = true }`,
		`unknown = { child = { value = '%USERPROFILE%' } }`,
	} {
		got, err := Restore([]byte(`unknown = 'changed'`), []byte(local))
		if err != nil || !reflect.DeepEqual(document(t, got), document(t, []byte(local))) {
			t.Fatalf("type change erased local path: %s %v", got, err)
		}
	}
	local := []byte(`items = [{ name = 'first', value = '/destination/path' }, { name = 'second' }]`)
	backup := []byte(`items = [{ name = 'second' }, { name = 'first' }]`)
	got, err := Restore(backup, local)
	if err != nil || !reflect.DeepEqual(document(t, got), document(t, local)) {
		t.Fatalf("reordered array moved local path: %s %v", got, err)
	}
}

func TestCapturePermissionAndArtifactUnits(t *testing.T) {
	source := []byte(`model = 'portable'
default_permissions = 'work'
sandbox_mode = 'danger-full-access'
[permissions.work]
extends = ':workspace'
filesystem = { ':workspace_roots' = { '**/*.env' = 'deny' }, '/source/work' = 'write' }
workspace_roots = { '/source/work' = true }
[skills]
max_context_tokens = 1000
config = [{ path = '../skill', enabled = false }, { path = 'other', enabled = true }]
[desktop.custom_file_handlers.editor]
command = 'editor'
icon = 'file:///source/icon.png'
label = 'Editor'
[otel.exporter.otlp-http]
endpoint = 'https://telemetry.example'
tls = { ca-certificate = 'certs/ca.pem', client-certificate = 'certs/client.pem' }
[marketplaces.local]
source_type = 'local'
source = '/source/plugins'
[plugins.'example@local']
enabled = true
`)
	backup, err := Capture(source)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"model": "portable", "skills": map[string]any{"max_context_tokens": int64(1000)}, "desktop": map[string]any{}}
	if !reflect.DeepEqual(document(t, backup), want) {
		t.Fatalf("partial machine policy or artifact copied: %s", backup)
	}
}

func TestURLExemptionKeepsAdjacentLocalReferences(t *testing.T) {
	for _, suffix := range []string{
		",/source/path", ";/source/path", ",C:/source/path", `,C:\source\path`,
		`,\\server\share`, `\source\path`, ",~/config", ",../config", ",file:///tmp/config",
		",$HOME/config", ";${ROOT}/config", ",%USERPROFILE%/config", "(/source/path)",
		"[/source/path]", "=/source/path", "`/source/path`",
	} {
		t.Run(suffix, func(t *testing.T) {
			value := "https://example.com/v1" + suffix
			wantErr := ErrUnclassifiedLocalReference
			if strings.Contains(suffix, "%USERPROFILE%") {
				// The existing credential guard rejects malformed URL escapes first.
				wantErr = ErrSecret
			}
			source, err := toml.Marshal(map[string]any{"unknown": value})
			if err != nil {
				t.Fatal(err)
			}
			for _, operation := range []func([]byte) ([]byte, error){Capture, func(b []byte) ([]byte, error) { return Restore(b, nil) }} {
				got, err := operation(source)
				if got != nil || !errors.Is(err, wantErr) {
					t.Fatalf("URL swallowed local suffix: %s %v", got, err)
				}
			}
			got, err := Restore([]byte("unknown = 'changed'"), source)
			if err != nil || !reflect.DeepEqual(document(t, got), document(t, source)) {
				t.Fatalf("local URL/path value erased: %s %v", got, err)
			}
		})
	}
}

func TestURLPathsAllowLiteralEnvironmentText(t *testing.T) {
	for _, value := range []string{
		"https://example.com/$HOME", "https://example.com/${ROOT}/config", "https://example.com/$env:USERPROFILE",
		"https://example.com/%25USERPROFILE%25", "https://[::1]:8443/$HOME", "See https://example.com/$HOME for help.",
		"https://example.com/a,https://example.com/b", "https://example.com/a;b",
	} {
		source, err := toml.Marshal(map[string]any{"unknown": value})
		if err != nil {
			t.Fatal(err)
		}
		got, err := Capture(source)
		if err != nil || !reflect.DeepEqual(document(t, got), document(t, source)) {
			t.Fatalf("public URL changed: %s %v", got, err)
		}
	}
}
