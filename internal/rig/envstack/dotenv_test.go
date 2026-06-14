package envstack

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDoubleQuotedValueKeepsAnEscapedQuote(t *testing.T) {
	// .env line:  KEY="a\"b c"   → value a"b c (the escaped quote isn't the end)
	env := Parse(`KEY="a\"b c"`)
	if got, want := env["KEY"], `a"b c`; got != want {
		t.Errorf("KEY = %q, want %q", got, want)
	}
}

func TestParsesBasicPairsCommentsAndBlanks(t *testing.T) {
	env := Parse("# a comment\nFOO=bar\n\nBAZ=qux")

	if len(env) != 2 {
		t.Errorf("len = %d, want 2", len(env))
	}
	if got := env["FOO"]; got != "bar" {
		t.Errorf("FOO = %q, want %q", got, "bar")
	}
	if got := env["BAZ"]; got != "qux" {
		t.Errorf("BAZ = %q, want %q", got, "qux")
	}
}

func TestStripsExportPrefix(t *testing.T) {
	env := Parse("export TOKEN=abc123")
	if got := env["TOKEN"]; got != "abc123" {
		t.Errorf("TOKEN = %q, want %q", got, "abc123")
	}
}

func TestDoubleQuotesHonourEscapesSingleQuotesAreLiteral(t *testing.T) {
	env := Parse("D=\"line1\\nline2\"\nS='line1\\nline2'")

	if got, want := env["D"], "line1\nline2"; got != want {
		t.Errorf("D = %q, want %q", got, want)
	}
	if got, want := env["S"], `line1\nline2`; got != want {
		t.Errorf("S = %q, want %q", got, want)
	}
}

func TestStripsInlineCommentOnUnquotedValueOnly(t *testing.T) {
	env := Parse("A=value # trailing comment\nB=\"value # not a comment\"")

	if got := env["A"]; got != "value" {
		t.Errorf("A = %q, want %q", got, "value")
	}
	if got, want := env["B"], "value # not a comment"; got != want {
		t.Errorf("B = %q, want %q", got, want)
	}
}

func TestSkipsInvalidKeys(t *testing.T) {
	env := Parse("1BAD=x\ngood_KEY=y\n=novalue")

	if _, ok := env["good_KEY"]; !ok {
		t.Error("good_KEY missing")
	}
	if _, ok := env["1BAD"]; ok {
		t.Error("1BAD should have been skipped")
	}
	if len(env) != 1 {
		t.Errorf("len = %d, want 1", len(env))
	}
}

func TestLoadOverlaysEnvLocalOverEnv(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "SHARED=base\nONLY_BASE=1")
	writeFile(t, dir, ".env.local", "SHARED=override\nONLY_LOCAL=2")

	env, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := env["SHARED"]; got != "override" {
		t.Errorf("SHARED = %q, want %q", got, "override")
	}
	if got := env["ONLY_BASE"]; got != "1" {
		t.Errorf("ONLY_BASE = %q, want %q", got, "1")
	}
	if got := env["ONLY_LOCAL"]; got != "2" {
		t.Errorf("ONLY_LOCAL = %q, want %q", got, "2")
	}
}

func TestLoadReturnsEmptyWhenNoFiles(t *testing.T) {
	env, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(env) != 0 {
		t.Errorf("len = %d, want 0", len(env))
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
