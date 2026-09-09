package process

import "context"

type supervisorKey struct{}

type supervisorCommand struct {
	path  string
	args  []string
	lease context.Context
}

// WithSupervisor selects an explicit executable entry point that calls
// ServeSupervisor and exits with its result. Unix retained commands then run in
// a separate supervisor that inherits the context's staging lease. Windows keeps
// its native job ownership. This is internal worker plumbing, not a shell command
// or a general remote-execution protocol. Do not enable queued hooks until the
// remaining supervisor-failure/restart fencing gates are complete.
func WithSupervisor(ctx context.Context, executable string, args ...string) context.Context {
	return context.WithValue(ctx, supervisorKey{}, supervisorCommand{
		path: executable, args: append([]string(nil), args...),
	})
}

// WithSupervisorLease attaches the staging lease solely to command supervision.
// The returned context keeps the operation's cancellation and ordinary lock
// identity, so private artifact/queue stores can acquire their own leases.
// It does not acquire ownership; the caller must retain staging until all
// commands finish. Without a selected supervisor this returns ctx unchanged.
func WithSupervisorLease(ctx, staging context.Context) context.Context {
	supervisor, ok := ctx.Value(supervisorKey{}).(supervisorCommand)
	if !ok {
		return ctx
	}
	supervisor.lease = staging
	return context.WithValue(ctx, supervisorKey{}, supervisor)
}
