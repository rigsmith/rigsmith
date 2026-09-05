package bridge

import (
	"context"
	"errors"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Event names the frontend subscribes to. Namespaced so they can't collide with
// anything Wails emits.
const (
	EventActionStart = "clauderig:action:start"
	EventActionLine  = "clauderig:action:line"
	EventActionDone  = "clauderig:action:done"
)

// Actions is the write half of the engine seam.
//
// Every operation here shells out to the clauderig binary rather than calling
// internal/clauderig in-process. That is deliberate and worth restating: sync,
// pull and merge carry the tripwire, the live-session guard, the merge
// policies, and the journal. Reimplementing any of that behind a button would
// mean two implementations of something that can lose data, and the UI's copy
// would be the one nobody tests.
type Actions struct {
	run *runner
	// emit is injected so tests exercise the service without an app running.
	emit func(name string, data any)
}

// NewActions builds the actions service, emitting to the running app.
func NewActions() *Actions {
	return NewActionsWithEmit(func(name string, data any) {
		if app := application.Get(); app != nil {
			app.Event.Emit(name, data)
		}
	})
}

// NewActionsWithEmit builds the service against a custom sink. Exported so the
// action path can be driven — and its streamed output inspected — without a
// window running.
func NewActionsWithEmit(emit func(name string, data any)) *Actions {
	return &Actions{run: newRunner(), emit: emit}
}

// Done is the terminal event for one action.
type Done struct {
	Action string `json:"action"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

// Run executes an action, streaming its output to the window as it arrives and
// emitting a Done when it finishes.
//
// It returns promptly on a rejected action (unknown verb, already busy) so the
// frontend can show that inline; a started action always reports through
// EventActionDone instead, because the caller has no useful way to wait.
func (a *Actions) Run(ctx context.Context, name string) error {
	return a.RunWith(ctx, name, "")
}

// RunWith is Run for the actions that take an id — materialising a session,
// switching an account. The id is validated against a narrow charset before it
// reaches a command line; see argvFor.
func (a *Actions) RunWith(ctx context.Context, name, arg string) error {
	action := Action(name)
	if !Allowed(action) {
		return errors.New("unknown action: " + name)
	}
	if _, err := argvFor(action, arg); err != nil {
		return err
	}
	if a.run.busy() {
		return ErrBusy
	}

	a.emit(EventActionStart, map[string]string{"action": name})

	go func() {
		// Deliberately not ctx: that context belongs to the frontend call and
		// is cancelled the moment Run returns, which would kill the command
		// mid-sync. An action outlives the request that asked for it.
		err := a.run.run(context.Background(), action, arg, func(l Line) {
			a.emit(EventActionLine, l)
		})

		done := Done{Action: name, OK: err == nil}
		if err != nil {
			done.Error = err.Error()
			// A nonzero exit is the CLI reporting a real outcome — a refused
			// sync, a merge with residual conflicts — and its own output has
			// already streamed. Repeating "exit status 1" as a separate line
			// would add noise, so it rides on the Done event only.
		}
		a.emit(EventActionDone, done)
	}()
	return nil
}

// Busy reports whether an action is in flight, so a reopened window can restore
// its buttons correctly.
func (a *Actions) Busy(ctx context.Context) bool { return a.run.busy() }
