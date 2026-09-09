//go:build linux || darwin

package service_test

import (
	"context"
	"os"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/process"
)

func queuedSupervisorContext(t *testing.T) context.Context {
	t.Helper()
	// Keep race instrumentation without a synthetic second of sleep per Git call.
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	return process.WithSupervisor(t.Context(), os.Args[0], "-test.run=^TestServiceSupervisorEntrypoint$")
}

func TestServiceSupervisorEntrypoint(t *testing.T) {
	if len(os.Args) != 2 || os.Args[1] != "-test.run=^TestServiceSupervisorEntrypoint$" {
		return
	}
	os.Exit(process.ServeSupervisor())
}

func TestQueueAdapterSupervisedRunnerStopAndDrain(t *testing.T) {
	// Includes startup history, capture, private artifact stores, commit, push,
	// confirmation, queue persistence, graceful stop and a fresh runner drain.
	queueAdapterRunnerStopAndDrain(t, queuedSupervisorContext(t))
}

func TestQueueAdapterSupervisedOfflineRecovery(t *testing.T) {
	// Reopen a committed batch after a transport failure and source removal;
	// supervision must retain the lease on retry without recapturing the source.
	queueAdapterOfflineRecovery(t, queuedSupervisorContext(t))
}
