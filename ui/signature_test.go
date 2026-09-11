package main

import (
	"testing"

	"github.com/rigsmith/rigsmith/ui/bridge"
)

// The tray menu is rebuilt when this string changes, so anything it reacts to
// that is not a real change is a menu rewritten under the hand reaching for it.
// Nothing promises the process scan returns windows in a stable order.
func TestWindowSignatureIgnoresScanOrder(t *testing.T) {
	a := bridge.DesktopView{Windows: []bridge.DesktopWindow{
		{PID: 2, Profile: "work"},
		{PID: 1, Main: true},
	}}
	b := bridge.DesktopView{Windows: []bridge.DesktopWindow{
		{PID: 1, Main: true},
		{PID: 2, Profile: "work"},
	}}
	if windowSignature(a) != windowSignature(b) {
		t.Errorf("a reshuffle read as a change:\n %q\n %q", windowSignature(a), windowSignature(b))
	}
}

// A window opening, closing, or becoming nameable IS a change.
func TestWindowSignatureNoticesRealChanges(t *testing.T) {
	base := bridge.DesktopView{Windows: []bridge.DesktopWindow{{PID: 1, Main: true}}}
	for name, other := range map[string]bridge.DesktopView{
		"another window": {Windows: []bridge.DesktopWindow{{PID: 1, Main: true}, {PID: 2}}},
		"a new name":     {Windows: []bridge.DesktopWindow{{PID: 1, Profile: "work"}}},
		"a new pid":      {Windows: []bridge.DesktopWindow{{PID: 9, Main: true}}},
		"nothing open":   {},
	} {
		if windowSignature(base) == windowSignature(other) {
			t.Errorf("%s read as no change", name)
		}
	}
}

// A label can carry a path, and a path can carry the separators — so two
// different machines must not encode to one string.
func TestWindowSignatureSeparatesAmbiguousLabels(t *testing.T) {
	a := bridge.DesktopView{Windows: []bridge.DesktopWindow{{PID: 1, DataDir: `/a";2="/b`}}}
	b := bridge.DesktopView{Windows: []bridge.DesktopWindow{
		{PID: 1, DataDir: "/a"}, {PID: 2, DataDir: "/b"},
	}}
	if windowSignature(a) == windowSignature(b) {
		t.Error("a label containing the separators collided with two windows")
	}
}

// A failed scan is its own state: it must not read as "the same windows as
// last time" and leave a stale menu up.
func TestWindowSignatureDistinguishesAFailedScan(t *testing.T) {
	if windowSignature(bridge.DesktopView{Error: "pgrep exploded"}) == windowSignature(bridge.DesktopView{}) {
		t.Error("a failed scan and an empty machine share a signature")
	}
}
