//go:build linux || darwin

package service_test

import "testing"

func TestQueueRuntimeSupervisedRestart(t *testing.T) {
	queueRuntimeRestart(t, queuedSupervisorContext(t))
}
