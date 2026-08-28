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
	mu      sync.RWMutex
	openers map[string]func()
	hiders  map[string]func()
}

// NewWindows builds the service with no windows registered; main adds them as
// it creates them.
func NewWindows() *Windows {
	return &Windows{openers: map[string]func(){}, hiders: map[string]func(){}}
}

// Register names a window the frontend may open and dismiss.
func (w *Windows) Register(name string, open, hide func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.openers[name] = open
	w.hiders[name] = hide
}

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
