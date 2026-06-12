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
