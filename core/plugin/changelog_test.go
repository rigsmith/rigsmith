package plugin

import (
	"context"
	"os"
	"testing"
)

// A plugin's entry ends in exactly one newline, as the built-in's does,
// however many it printed: the next entry or title starts on its own line.
func TestSubprocessChangelogEndsInOneNewline(t *testing.T) {
	for _, printed := range []string{"## 1.0.0\n\n- A change", "## 1.0.0\n\n- A change\n", "## 1.0.0\n\n- A change\n\n\n"} {
		t.Setenv("RIGSMITH_HELPER_PLUGIN", printed)
		g := NewSubprocessChangelogGenerator("helper", &Host{Path: os.Args[0], BaseArgs: []string{"-test.run=^TestHelperPlugin$", "--"}})
		got, err := g.Render(context.Background(), ChangelogRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if got != "## 1.0.0\n\n- A change\n" {
			t.Errorf("printed %q, rendered %q", printed, got)
		}
	}
}
