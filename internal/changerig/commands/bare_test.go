package commands

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestBareRunESoftensOnlyTheBareRun pins which invocations get orientation and
// which keep the failure: merely running the binary in an unconfigured
// directory is not an error, but asking it to do something there still is.
func TestBareRunESoftensOnlyTheBareRun(t *testing.T) {
	notSetUp := fmt.Errorf("%w — run `changerig init` to create .changeset/", ErrNotSetUp)
	handler := func(*cobra.Command, []string) error { return notSetUp }

	t.Run("bare run exits 0 with the guidance", func(t *testing.T) {
		var out bytes.Buffer
		cmd := &cobra.Command{Use: "changerig"}
		cmd.SetOut(&out)
		if err := BareRunE(handler)(cmd, nil); err != nil {
			t.Fatalf("bare run returned %v, want nil (exit 0)", err)
		}
		if !strings.Contains(out.String(), "not set up here yet") {
			t.Errorf("output = %q, want the setup guidance", out.String())
		}
	})

	t.Run("args keep the error", func(t *testing.T) {
		cmd := &cobra.Command{Use: "changerig"}
		cmd.SetOut(&bytes.Buffer{})
		if err := BareRunE(handler)(cmd, []string{"something"}); !errors.Is(err, ErrNotSetUp) {
			t.Errorf("err = %v, want the error preserved when the user asked for something", err)
		}
	})

	t.Run("flags keep the error", func(t *testing.T) {
		cmd := &cobra.Command{Use: "changerig"}
		cmd.SetOut(&bytes.Buffer{})
		cmd.Flags().StringP("message", "m", "", "")
		if err := cmd.Flags().Set("message", "a change"); err != nil {
			t.Fatal(err)
		}
		if err := BareRunE(handler)(cmd, nil); !errors.Is(err, ErrNotSetUp) {
			t.Errorf("err = %v, want the error preserved for `changerig -m …`", err)
		}
	})

	t.Run("unrelated errors pass through", func(t *testing.T) {
		boom := errors.New("boom")
		cmd := &cobra.Command{Use: "changerig"}
		cmd.SetOut(&bytes.Buffer{})
		if err := BareRunE(func(*cobra.Command, []string) error { return boom })(cmd, nil); !errors.Is(err, boom) {
			t.Errorf("err = %v, want unrelated errors untouched", err)
		}
	})

	t.Run("success passes through", func(t *testing.T) {
		cmd := &cobra.Command{Use: "changerig"}
		cmd.SetOut(&bytes.Buffer{})
		if err := BareRunE(func(*cobra.Command, []string) error { return nil })(cmd, nil); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})
}
