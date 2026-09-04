package jsonc

import (
	"strings"
	"testing"
)

func TestDelete(t *testing.T) {
	src := `{
  // the manifest
  "repos": {
    // pty-core: the library
    "pty-core": {
      "upstream": "github.com/acme/pty-core", // trailing
      "fork": "github.com/you/pty-core"
    },
    "term-core": { "upstream": "github.com/acme/term-core" }
  },
  "lastSync": { "pty-core": "abc", "term-core": "def" },
  "one": 1
}`
	t.Run("a nested member goes with its comment lines and nothing else", func(t *testing.T) {
		got, ok := Delete(src, []string{"repos", "pty-core"})
		if !ok {
			t.Fatal("Delete returned false")
		}
		mustNotContain(t, got, "pty-core: the library")
		mustNotContain(t, got, `"pty-core": {`)
		mustNotContain(t, got, "// trailing")
		mustContain(t, got, "// the manifest")
		mustContain(t, got, `"term-core": { "upstream"`)
		mustContain(t, got, `"lastSync": { "pty-core": "abc"`) // a different pty-core key, untouched
		var m map[string]any
		if err := Unmarshal([]byte(got), &m); err != nil {
			t.Fatalf("not valid: %v\n%s", err, got)
		}
		if _, still := m["repos"].(map[string]any)["pty-core"]; still {
			t.Fatalf("still present:\n%s", got)
		}
		if strings.Contains(got, "\n\n    \"term-core\"") {
			t.Errorf("blank line left where the member was:\n%s", got)
		}
	})

	t.Run("the last member takes the comma before it", func(t *testing.T) {
		got, ok := Delete(src, []string{"repos", "term-core"})
		if !ok {
			t.Fatal("Delete returned false")
		}
		var m map[string]any
		if err := Unmarshal([]byte(got), &m); err != nil {
			t.Fatalf("not valid: %v\n%s", err, got)
		}
		repos := m["repos"].(map[string]any)
		if len(repos) != 1 || repos["pty-core"] == nil {
			t.Fatalf("repos = %v", repos)
		}
	})

	t.Run("a single-line object stays on one line", func(t *testing.T) {
		got, ok := Delete(src, []string{"lastSync", "pty-core"})
		if !ok {
			t.Fatal("Delete returned false")
		}
		mustContain(t, got, `"lastSync": { "term-core": "def" }`)
		got, ok = Delete(got, []string{"lastSync", "term-core"})
		if !ok {
			t.Fatal("Delete returned false")
		}
		mustContain(t, got, `"lastSync": {  }`)
	})

	t.Run("a top-level member", func(t *testing.T) {
		got, ok := Delete(src, []string{"one"})
		if !ok {
			t.Fatal("Delete returned false")
		}
		mustNotContain(t, got, `"one"`)
		var m map[string]any
		if err := Unmarshal([]byte(got), &m); err != nil {
			t.Fatalf("not valid: %v\n%s", err, got)
		}
	})

	t.Run("a member three deep, as an inline stack block keeps them", func(t *testing.T) {
		doc := `{
  "stack": {
    "repos": {
      "pty-core": { "upstream": "a" },
      "term-core": { "upstream": "b" }
    },
    "lastSync": { "pty-core": "abc" }
  },
  "repos": { "pty-core": "decoy at the wrong depth" }
}`
		got, ok := Delete(doc, []string{"stack", "repos", "pty-core"})
		if !ok {
			t.Fatal("Delete returned false")
		}
		var m map[string]any
		if err := Unmarshal([]byte(got), &m); err != nil {
			t.Fatalf("not valid: %v\n%s", err, got)
		}
		repos := m["stack"].(map[string]any)["repos"].(map[string]any)
		if _, still := repos["pty-core"]; still || repos["term-core"] == nil {
			t.Fatalf("repos = %v", repos)
		}
		mustContain(t, got, `"lastSync": { "pty-core": "abc" }`)
		mustContain(t, got, `"decoy at the wrong depth"`)
	})

	t.Run("an absent member is a no-op success", func(t *testing.T) {
		got, ok := Delete(src, []string{"repos", "nope"})
		if !ok || got != src {
			t.Fatalf("ok=%v changed=%v", ok, got != src)
		}
	})

	t.Run("malformed input and non-object parents are refused", func(t *testing.T) {
		if _, ok := Delete(`{"a": `, []string{"a"}); ok {
			t.Error("accepted malformed input")
		}
		if _, ok := Delete(`{"a": [1]}`, []string{"a", "b"}); ok {
			t.Error("accepted an array parent")
		}
		if _, ok := Delete(`[1]`, []string{"a"}); ok {
			t.Error("accepted an array root")
		}
	})
}
