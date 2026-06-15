package envstack

import "testing"

func envMap(pairs ...[2]string) map[string]string {
	d := make(map[string]string)
	for _, p := range pairs {
		set(d, p[0], p[1])
	}
	return d
}

func TestPrecedenceIsFileThenAmbientThenConfigThenCommand(t *testing.T) {
	fileEnv := envMap([2]string{"A", "file"}, [2]string{"B", "file"}, [2]string{"C", "file"}, [2]string{"D", "file"})
	ambient := envMap([2]string{"B", "ambient"}, [2]string{"C", "ambient"}, [2]string{"D", "ambient"})
	config := envMap([2]string{"C", "config"}, [2]string{"D", "config"})
	command := envMap([2]string{"D", "command"})

	merged := Merge(fileEnv, ambient, config, command)

	if got := merged["A"]; got != "file" { // only in file
		t.Errorf("A = %q, want %q", got, "file")
	}
	if got := merged["B"]; got != "ambient" { // ambient beats file (dotenv-style)
		t.Errorf("B = %q, want %q", got, "ambient")
	}
	if got := merged["C"]; got != "config" { // config beats ambient
		t.Errorf("C = %q, want %q", got, "config")
	}
	if got := merged["D"]; got != "command" { // per-command wins
		t.Errorf("D = %q, want %q", got, "command")
	}
}

func TestLookup(t *testing.T) {
	env := envMap([2]string{"NPM_TOKEN", "x"}, [2]string{"EMPTY", ""})

	if v, ok := Lookup(env, "NPM_TOKEN"); !ok || v != "x" {
		t.Errorf("Lookup(NPM_TOKEN) = %q,%v; want \"x\",true", v, ok)
	}
	// Present-but-empty is found (so ${env.EMPTY} resolves to "").
	if v, ok := Lookup(env, "EMPTY"); !ok || v != "" {
		t.Errorf("Lookup(EMPTY) = %q,%v; want \"\",true", v, ok)
	}
	// Absent is not found.
	if v, ok := Lookup(env, "ABSENT"); ok || v != "" {
		t.Errorf("Lookup(ABSENT) = %q,%v; want \"\",false", v, ok)
	}
	// A nil map never panics and reports absence.
	if v, ok := Lookup(nil, "ANYTHING"); ok || v != "" {
		t.Errorf("Lookup(nil) = %q,%v; want \"\",false", v, ok)
	}
	// On case-insensitive platforms (Windows) a different-cased key still
	// matches; elsewhere it does not. Assert the active platform's behaviour.
	gotV, gotOK := Lookup(env, "npm_token")
	if caseInsensitiveKeys {
		if !gotOK || gotV != "x" {
			t.Errorf("Lookup(npm_token) on Windows = %q,%v; want \"x\",true", gotV, gotOK)
		}
	} else if gotOK {
		t.Errorf("Lookup(npm_token) = %q,%v; want a miss on a case-sensitive platform", gotV, gotOK)
	}
}

func TestNilLayersAreIgnored(t *testing.T) {
	merged := Merge(nil, envMap([2]string{"X", "1"}), nil, nil)
	got, ok := merged["X"]
	if !ok {
		t.Fatal("X missing")
	}
	if got != "1" {
		t.Errorf("X = %q, want %q", got, "1")
	}
}
