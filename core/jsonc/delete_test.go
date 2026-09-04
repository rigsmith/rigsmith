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

	t.Run("the previous member keeps its trailing comment", func(t *testing.T) {
		doc := "{\n  \"a\": 1, // about a\n  // about b\n  \"b\": 2\n}"
		got, ok := Delete(doc, []string{"b"})
		if !ok {
			t.Fatal("Delete returned false")
		}
		if got != "{\n  \"a\": 1 // about a\n}" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("a JSONC trailing comma goes with the only member", func(t *testing.T) {
		got, ok := Delete("{\n  \"repos\": {\n    \"a\": 1,\n  }\n}", []string{"repos", "a"})
		if !ok {
			t.Fatal("Delete returned false")
		}
		if strings.Contains(got, ",") {
			t.Fatalf("naked comma left behind: %q", got)
		}
		var m map[string]any
		if err := Unmarshal([]byte(got), &m); err != nil {
			t.Fatalf("not valid: %v\n%s", err, got)
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

// A byte-order mark survives the edit, and a path that is absent hands the
// input back untouched, mark included.
func TestDelete_KeepsTheBOM(t *testing.T) {
	in := "\uFEFF{\n  \"a\": 1,\n  \"b\": 2\n}\n"
	got, ok := Delete(in, []string{"b"})
	if !ok || got != "\uFEFF{\n  \"a\": 1\n}\n" {
		t.Fatalf("got %q, %v", got, ok)
	}
	if got, ok := Delete(in, []string{"zzz"}); !ok || got != in {
		t.Fatalf("absent path: got %q, %v; want the input back", got, ok)
	}
}

// Deleting the only member takes its trailing comment with it, on its own
// line or beside the brace.
func TestDelete_OnlyMemberTakesItsComment(t *testing.T) {
	for in, want := range map[string]string{
		"{\n  \"a\": 1 // about a\n}\n": "{\n}\n",
		"{ \"a\": 1 // about a\n}\n":    "{ \n}\n",
		"{ \"a\": 1 /* a */ }\n":        "{ }\n",
	} {
		got, ok := Delete(in, []string{"a"})
		if !ok || got != want {
			t.Errorf("Delete(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}
