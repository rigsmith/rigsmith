package bridge

import (
	"context"
	"fmt"
	"sync"
)

// Windows lets the frontend raise one of the app's other windows.
//
// The windows themselves live in package main, which is where the Wails
// application is built and where "show it and focus it" is already defined.
// This service is only the doorbell: main registers a named opener, the
// frontend rings it. Keeping the binding here rather than in main means
// binding_test.go covers it like every other bound method — a renamed window
// would otherwise compile cleanly and fail in a live window.
type Windows struct {
	mu       sync.RWMutex
	openers  map[string]func()
	hiders   map[string]func()
	resizers map[string]func(int)
	modes    map[string]string
}

// NewWindows builds the service with no windows registered; main adds them as
// it creates them.
func NewWindows() *Windows {
	return &Windows{
		openers:  map[string]func(){},
		hiders:   map[string]func(){},
		resizers: map[string]func(int){},
		modes:    map[string]string{},
	}
}

// Register names a window the frontend may open and dismiss.
func (w *Windows) Register(name string, open, hide func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.openers[name] = open
	w.hiders[name] = hide
}

// SetMode records the pane a window is being opened on, for the page to collect
// when it is ready.
//
// An event cannot carry this on its own. The first open of a window is also the
// first load of its page, and an event emitted immediately after Show arrives
// before there is a listener — silently, since nobody is there to miss it. That
// failure is invisible for a window whose default pane is the one being asked
// for, and total for the other, which is exactly how it presented: "list"
// looked fine and "places" did nothing.
func (w *Windows) SetMode(name, mode string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.modes[name] = mode
}

// Mode hands over the pane that window was last asked for, and forgets it.
//
// Consumed on read, not left behind: the next plain open of the window — from
// the tray, from another window's button — is not a request for whichever pane
// somebody chose an hour ago.
func (w *Windows) Mode(ctx context.Context, name string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	mode := w.modes[name]
	delete(w.modes, name)
	return mode, nil
}

// Resizable names a window that may ask to be as tall as its contents.
func (w *Windows) Resizable(name string, resize func(height int)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.resizers[name] = resize
}

// Fit makes a window as tall as the content it just drew.
//
// A popover has no business being a fixed size: it lists however many Desktop
// profiles this machine has, which is two on one and six on another, and a
// window sized for the larger shows the smaller a panel of empty background.
// The page is the only thing that knows how tall it ended up, so it asks.
//
// Bounded here rather than trusted: the height arrives from a page, and a
// window that is one pixel tall or taller than the display is not a state any
// caller should be able to ask for.
func (w *Windows) Fit(ctx context.Context, name string, height int) error {
	w.mu.RLock()
	resize, ok := w.resizers[name]
	w.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no resizable window named %q", name)
	}
	resize(min(max(height, minPopoverHeight), maxPopoverHeight))
	return nil
}

// The range a popover may ask for. The floor keeps a failed render from
// collapsing the window to a sliver; the ceiling keeps a machine with twenty
// profiles from drawing a popover the length of the screen, which is a list
// that should scroll rather than a window that should grow.
const (
	minPopoverHeight = 140
	maxPopoverHeight = 560
)

// Open shows the named window. An unknown name is an error rather than a
// silent no-op: a button that does nothing reads as a broken app.
func (w *Windows) Open(ctx context.Context, name string) error {
	w.mu.RLock()
	open, ok := w.openers[name]
	w.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no window named %q", name)
	}
	open()
	return nil
}

// Hide dismisses the named window.
//
// It exists so moving between the two windows can be a swap rather than a
// stack. Raising one window over another turned out to be unreliable: the click
// that asks for the new window hands focus back to the old one as it finishes,
// and neither re-focusing nor lifting the window level settled it. Hiding the
// one you are leaving has no such race — there is nothing left to be behind.
// Both windows carry a button back to the other, so the swap is reversible.
func (w *Windows) Hide(ctx context.Context, name string) error {
	w.mu.RLock()
	hide, ok := w.hiders[name]
	w.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no window named %q", name)
	}
	hide()
	return nil
}
