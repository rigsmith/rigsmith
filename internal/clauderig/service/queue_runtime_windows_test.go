package service_test

import (
	"github.com/rigsmith/rigsmith/internal/agentrig/process"
	"testing"
)

func TestQueueRuntimeSupervisedRestart(t *testing.T) {
	queueRuntimeRestart(t, process.WithSupervisor(t.Context(), "unused-on-windows"))
}
