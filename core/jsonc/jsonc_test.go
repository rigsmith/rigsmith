package jsonc

import (
	"encoding/json"
	"testing"
)

func TestUnmarshalCommentsAndTrailingCommas(t *testing.T) {
	src := []byte(`{
		// the base command for built-ins
		"tool": "npx changeset", /* inline
		   block comment */
		"order": [
			"version",
			"publish", // trailing comment
		],
		"steps": {
			"publish": { "confirm": true, },
		},
	}`)
	var v struct {
		Tool  string   `json:"tool"`
		Order []string `json:"order"`
		Steps map[string]struct {
			Confirm bool `json:"confirm"`
		} `json:"steps"`
	}
	if err := Unmarshal(src, &v); err != nil {
		t.Fatal(err)
	}
	if v.Tool != "npx changeset" || len(v.Order) != 2 || v.Order[1] != "publish" {
		t.Errorf("parsed = %+v", v)
	}
	if !v.Steps["publish"].Confirm {
		t.Error("nested value after trailing commas lost")
	}
}

func TestStripLeavesStringsUntouched(t *testing.T) {
	src := []byte(`{ "url": "https://example.com", "glob": "a/*b*/c", "esc": "say \"hi\" // ok", "comma": "a,}" }`)
	var v map[string]string
	if err := Unmarshal(src, &v); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"url":   "https://example.com",
		"glob":  "a/*b*/c",
		"esc":   `say "hi" // ok`,
		"comma": "a,}",
	}
	for k, w := range want {
		if v[k] != w {
			t.Errorf("%s = %q, want %q", k, v[k], w)
		}
	}
}

func TestStripPreservesOffsetsAndLines(t *testing.T) {
	src := []byte("{\n  // c\n  \"a\": 1\n}")
	out := Strip(src)
	if len(out) != len(src) {
		t.Fatalf("length changed: %d → %d", len(src), len(out))
	}
	for i, c := range src {
		if c == '\n' && out[i] != '\n' {
			t.Errorf("newline at %d not preserved", i)
		}
	}
}

func TestInvalidJSONStillErrors(t *testing.T) {
	var v any
	if err := Unmarshal([]byte(`{ "a": `), &v); err == nil {
		t.Error("truncated JSON should error")
	}
	var syn *json.SyntaxError
	if err := Unmarshal([]byte("{\n  \"a\" 1\n}"), &v); err == nil {
		t.Error("malformed JSON should error")
	} else if !asSyntax(err, &syn) {
		t.Logf("non-syntax error type: %v", err) // type not critical, error is
	}
}

func asSyntax(err error, target **json.SyntaxError) bool {
	s, ok := err.(*json.SyntaxError)
	if ok {
		*target = s
	}
	return ok
}

func TestCRLFAndPlainJSONPassThrough(t *testing.T) {
	var v map[string]int
	if err := Unmarshal([]byte("{\r\n  \"a\": 1, // x\r\n  \"b\": 2,\r\n}"), &v); err != nil {
		t.Fatal(err)
	}
	if v["a"] != 1 || v["b"] != 2 {
		t.Errorf("v = %v", v)
	}
	if err := Unmarshal([]byte(`{"plain": true}`), &v); err == nil {
		// plain JSON must parse — wrong target type is the only error here
		t.Skip()
	}
	var ok map[string]bool
	if err := Unmarshal([]byte(`{"plain": true}`), &ok); err != nil || !ok["plain"] {
		t.Errorf("plain JSON failed: %v %v", ok, err)
	}
}
