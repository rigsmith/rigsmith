package commands

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

// huhEscKeyMap must bind esc (and ctrl+c) to quit so wizards can be escaped —
// huh's default only quits on ctrl+c.
func TestHuhEscKeyMap_BindsEsc(t *testing.T) {
	keys := huhEscKeyMap().Quit.Keys()
	for _, want := range []string{"esc", "ctrl+c"} {
		if !slices.Contains(keys, want) {
			t.Errorf("Quit keys %v missing %q", keys, want)
		}
	}
}

// cancelOrErr turns a huh abort into a clean exit (nil + a note) while letting
// real errors through — so escaping the wizard doesn't print a red error box.
func TestCancelOrErr(t *testing.T) {
	var buf bytes.Buffer
	if err := cancelOrErr(&buf, huh.ErrUserAborted); err != nil {
		t.Fatalf("abort should map to nil, got %v", err)
	}
	if !strings.Contains(buf.String(), "cancelled") {
		t.Errorf("expected a cancelled note, got %q", buf.String())
	}

	sentinel := errors.New("boom")
	if err := cancelOrErr(&buf, sentinel); !errors.Is(err, sentinel) {
		t.Fatalf("non-abort error should pass through, got %v", err)
	}
}
