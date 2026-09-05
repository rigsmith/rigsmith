package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Chooser opens the native folder picker.
//
// A path typed into a text field is a path you can get wrong — and the failure
// is silent, because a session re-filed under a directory with a typo in it is
// simply filed somewhere else that looks plausible. The system picker only ever
// returns somewhere that exists.
type Chooser struct{}

// nativeDialogs counts the system dialogs currently up.
//
// The tray window hides when it loses focus, which is what a menu bar window
// should do — and a native dialog takes focus, so opening the folder picker
// dismissed the window that asked for it. The window's own auto-hide consults
// this rather than the picker reaching in and disabling it, so any future
// dialog gets the same protection by construction.
var nativeDialogs atomic.Int32

// NativeDialogOpen reports whether a system dialog is up, so the window's
// auto-hide can stand down while one is.
func NativeDialogOpen() bool { return nativeDialogs.Load() > 0 }

// NewChooser builds the chooser service.
func NewChooser() *Chooser { return &Chooser{} }

// Directory prompts for a folder, starting at start when it is a real directory.
// Returns "" when the dialog is cancelled, which is not an error — the caller
// asked a question and the answer was "never mind".
func (c *Chooser) Directory(ctx context.Context, window, title, start string) (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("no window to attach a dialog to")
	}
	if title == "" {
		title = "Choose a folder"
	}

	// Held across the prompt, which blocks until the user answers.
	nativeDialogs.Add(1)
	defer nativeDialogs.Add(-1)

	d := app.Dialog.OpenFile().
		SetTitle(title).
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(false)

	// Attached, so macOS runs it as a sheet belonging to the window rather than
	// as a free-floating panel. Unattached it opened BEHIND the window that
	// asked for it, because the tray window is raised on reveal and nothing put
	// the dialog above it. A sheet is always in front of its parent and moves
	// with it, which is also what makes it obvious which question is being
	// answered.
	if w, ok := app.Window.GetByName(window); ok && w != nil {
		d.AttachToWindow(w)
	}

	// Start where the session already is, so the common case — moving it one
	// level down — opens next to the answer rather than at the home directory.
	if dir := existingDir(start); dir != "" {
		d.SetDirectory(dir)
	}
	return d.PromptForSingleSelection()
}

// existingDir resolves start to a directory that exists, walking up if it does
// not: a session filed under a folder that has since been deleted should still
// open the picker somewhere useful rather than nowhere.
func existingDir(start string) string {
	start = strings.TrimSpace(start)
	if start == "" {
		return ""
	}
	if strings.HasPrefix(start, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		start = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(start, "~"), "/"))
	}
	for dir := filepath.Clean(start); dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}
