package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The thing a proposed branch does not say about itself: it references a
// package that only this stackspace answers, so a plain checkout of it cannot
// restore. Nothing reported that until restore failed, and NuGet can fail
// without printing why.
func TestStackStackOnlyPins(t *testing.T) {
	ctx := context.Background()

	t.Run("names what this member gets from a sibling", func(t *testing.T) {
		root := t.TempDir()
		csproj(t, root, "app/src/App", "Acme.App", "Acme.Lib")
		csproj(t, root, "lib/src/Lib", "Acme.Lib")

		links, failed := stackStackOnlyPins(ctx, root, owned("app", "lib"), "app")
		if len(failed) != 0 {
			t.Fatalf("scan failed: %v", failed)
		}
		if got := stackPinNames(links); len(got) != 1 || got[0] != "Acme.Lib" {
			t.Fatalf("pins = %v, want [Acme.Lib]", got)
		}
	})

	t.Run("says nothing about a member that consumes nothing here", func(t *testing.T) {
		root := t.TempDir()
		csproj(t, root, "app/src/App", "Acme.App", "Acme.Lib")
		csproj(t, root, "lib/src/Lib", "Acme.Lib")

		// lib is the producer, not a consumer — proposing it carries no pin
		// that the stackspace is answering.
		links, _ := stackStackOnlyPins(ctx, root, owned("app", "lib"), "lib")
		if len(links) != 0 {
			t.Fatalf("pins = %v, want none for the producing member", stackPinNames(links))
		}
	})

	t.Run("a republished id is the case most worth naming", func(t *testing.T) {
		// The consumer references the id the producer is republished under, so
		// no public feed carries it under that name at all — "is it on
		// nuget.org" is not even the right question.
		root := t.TempDir()
		csproj(t, root, "app/src/App", "Acme.App", "You.Lib")
		csproj(t, root, "lib/src/Lib", "Lib")
		m := owned("app", "lib")
		m.Repos["lib"].PublishesAs = map[string]string{"Lib": "You.Lib"}

		links, _ := stackStackOnlyPins(ctx, root, m, "app")
		if len(links) != 1 {
			t.Fatalf("pins = %v, want the republished id", stackPinNames(links))
		}
		if links[0].Via == "" {
			t.Errorf("the republishing is not recorded: %+v", links[0])
		}
	})
}

func TestStackReportStackOnlyPins(t *testing.T) {
	links := []stackLink{{From: []string{"app"}, To: "lib"}}
	links[0].Package = "Acme.Lib"

	t.Run("says what it is and what to do", func(t *testing.T) {
		var b bytes.Buffer
		stackReportStackOnlyPins(&b, links, "app", nil)
		got := b.String()
		for _, want := range []string{"app references 1 package", "Acme.Lib", "plain checkout", "pack it from the stackspace"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in:\n%s", want, got)
			}
		}
	})

	t.Run("silent when there is nothing to say", func(t *testing.T) {
		var b bytes.Buffer
		stackReportStackOnlyPins(&b, nil, "app", nil)
		if b.Len() != 0 {
			t.Fatalf("said something about no pins: %s", b.String())
		}
	})

	t.Run("silent when the scan could not look", func(t *testing.T) {
		// "No links found" and "the scan failed" are different answers, and
		// only the first may be reported as reassurance — the same distinction
		// the overlay check draws.
		var b bytes.Buffer
		stackReportStackOnlyPins(&b, links, "app", map[string]error{"dotnet": context.Canceled})
		if b.Len() != 0 {
			t.Fatalf("reported on a failed scan: %s", b.String())
		}
	})
}
