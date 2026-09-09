package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func testState(t *testing.T) (*uiState, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ui-state.json")
	return &uiState{path: func() (string, error) { return path, nil }}, path
}

// A setting nobody has expressed is not a setting turned off.
func TestUIStateDefaultsWhenTheFileIsMissing(t *testing.T) {
	st, _ := testState(t)
	if !st.Bool(desktopWarnKey, true) {
		t.Error("a missing state file must answer with the default, not false")
	}
}

func TestUIStateRoundTrips(t *testing.T) {
	st, _ := testState(t)
	if err := st.SetBool(desktopWarnKey, false); err != nil {
		t.Fatal(err)
	}
	if st.Bool(desktopWarnKey, true) {
		t.Error("the saved answer was not read back")
	}
	if err := st.SetBool(desktopWarnKey, true); err != nil {
		t.Fatal(err)
	}
	if !st.Bool(desktopWarnKey, false) {
		t.Error("turning the warning back on did not stick")
	}
}

// The reason the file is a map rather than a struct: the second setting must
// not cost somebody their first.
func TestUIStateKeepsSettingsItDoesNotKnowAbout(t *testing.T) {
	st, path := testState(t)
	if err := os.WriteFile(path, []byte(`{"somethingElse":"keep me"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.SetBool(desktopWarnKey, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]any{}
	if uerr := json.Unmarshal(body, &all); uerr != nil {
		t.Fatal(uerr)
	}
	if all["somethingElse"] != "keep me" {
		t.Errorf("writing one setting dropped another: %v", all)
	}
	if all[desktopWarnKey] != false {
		t.Errorf("%s = %v, want false", desktopWarnKey, all[desktopWarnKey])
	}
}

// A file that will not parse is a file we cannot read. Reading "off" out of it
// would silence a warning nobody switched off.
func TestUIStateFallsBackOnACorruptFile(t *testing.T) {
	st, path := testState(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !st.Bool(desktopWarnKey, true) {
		t.Error("a corrupt state file must answer with the default")
	}
	// And it must still be writable afterwards, or the setting could never be
	// repaired from the UI.
	if err := st.SetBool(desktopWarnKey, true); err != nil {
		t.Fatalf("could not write over a corrupt state file: %v", err)
	}
}

// Mute is the notice's own off switch, and Get has to report it so the button
// can stop offering what has already been done.
func TestDesktopMuteTurnsTheWarningOff(t *testing.T) {
	st, _ := testState(t)
	d := newTestDesktop(fakeDesktop{}, nil, nil)
	d.state = st

	if !d.Warn() {
		t.Fatal("warnings must be on by default")
	}
	if err := d.Mute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.Warn() {
		t.Error("Mute did not turn the warning off")
	}
	v, err := d.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Warn {
		t.Error("the view still reports warnings as on after Mute")
	}
	if err := d.SetWarn(true); err != nil {
		t.Fatal(err)
	}
	if !d.Warn() {
		t.Error("the tray checkbox could not turn the warning back on")
	}
}
