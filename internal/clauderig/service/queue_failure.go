package service

import (
	"context"
	"errors"
	"io/fs"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
)

// Capture errors distinguish changing bytes from unavailable requested data.
var (
	ErrCaptureSourceChanged     = errors.New("capture source changed")
	ErrCaptureSourceUnavailable = errors.New("requested capture source unavailable")
)

// Classify only service errors, never queue phase-marker/acknowledgement writes.
// Permanent checks precede transport markers because errors may be joined.
func (s Service) queueFailure(ctx context.Context, work queue.Work, err error) error {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, queue.ErrUncertain) {
		return err
	}
	code, retry := "operation-failed", false
	switch {
	case errors.Is(err, engine.ErrSecretTripwire):
		code = "scan-rejected"
	case errors.Is(err, queue.ErrBinding):
		code = "binding-mismatch"
	case errors.Is(err, queue.ErrTransition):
		code = "invalid-work"
	case errors.Is(err, commitartifact.ErrConflict):
		code = "publication-conflict"
	case errors.Is(err, artifact.ErrInvalid), errors.Is(err, commitartifact.ErrInvalid), errors.Is(err, commitartifact.ErrAttributes):
		code = "artifact-invalid"
	case errors.Is(err, artifact.ErrTooLarge):
		code = "capacity-exceeded"
	case errors.Is(err, ErrCaptureSourceUnavailable), errors.Is(err, fs.ErrNotExist):
		code = "data-unavailable"
	case errors.Is(err, fs.ErrPermission):
		code = "permission-denied"
	case errors.Is(err, ErrCaptureSourceChanged):
		code, retry = "source-changing", true
	case errors.Is(err, storelock.ErrBusy):
		code, retry = "store-busy", true
	case errors.Is(err, context.DeadlineExceeded):
		code, retry = "operation-timeout", true
	case errors.Is(err, commitartifact.ErrTransport), errors.Is(err, commitartifact.ErrUnconfirmed):
		code, retry = "publication-unconfirmed", true
	}
	now := s.now()
	if retry {
		return queue.RetryFailure(now, work.Attempts, code, err)
	}
	return &queue.ExecutionFailure{Code: code, RetryAt: now, Blocked: true, Cause: err}
}
