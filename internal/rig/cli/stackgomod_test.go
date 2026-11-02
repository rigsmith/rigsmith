package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestStackWireMalformedGoMember(t *testing.T) {
	root := t.TempDir()
	for path, body := range map[string]string{
		"member/_ignored/go.mod":     "module (\n",
		"member/.hidden/go.mod":      "module (\n",
		"member/testdata/bad/go.mod": "module (\n",
		"member/lib/go.mod":          "module (\n \"example.com/lib\"\n)\n",
		"outside/go.mod":             "go 1.20\n",
	} {
		dest := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	stackWarnMalformedGoMembers(&out, root, []string{"member"}, "")
	if got := out.String(); got != "" {
		t.Fatalf("fixture warnings = %s", got)
	}
	path := filepath.Join(root, "member", "go.mod")
	if err := os.WriteFile(path, []byte("module (\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stackWarnMalformedGoMembers(&out, root, []string{"member"}, "")
	want := "warning: " + filepath.Join("member", "go.mod") + ": missing or malformed module directive\n"
	if got := out.String(); got != want {
		t.Fatalf("warnings = %q, want %q", got, want)
	}
}
