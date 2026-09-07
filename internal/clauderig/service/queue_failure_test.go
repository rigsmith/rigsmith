package service

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
)

func TestQueueFailureClassification(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	s := Service{Now: func() time.Time { return now }}
	for _, tc := range []struct {
		err   error
		code  string
		retry bool
	}{
		{engine.ErrSecretTripwire, "scan-rejected", false},
		{queue.ErrBinding, "binding-mismatch", false},
		{queue.ErrTransition, "invalid-work", false},
		{commitartifact.ErrConflict, "publication-conflict", false},
		{artifact.ErrInvalid, "artifact-invalid", false},
		{commitartifact.ErrInvalid, "artifact-invalid", false},
		{commitartifact.ErrAttributes, "artifact-invalid", false},
		{artifact.ErrTooLarge, "capacity-exceeded", false},
		{fs.ErrNotExist, "data-unavailable", false},
		{ErrCaptureSourceUnavailable, "data-unavailable", false},
		{fs.ErrPermission, "permission-denied", false},
		{ErrCaptureSourceChanged, "source-changing", true},
		{storelock.ErrBusy, "store-busy", true},
		{context.DeadlineExceeded, "operation-timeout", true},
		{commitartifact.ErrTransport, "publication-unconfirmed", true},
		{commitartifact.ErrUnconfirmed, "publication-unconfirmed", true},
		{errors.New("unrecognized private error"), "operation-failed", false},
		{errors.Join(commitartifact.ErrUnconfirmed, engine.ErrSecretTripwire), "scan-rejected", false},
		{errors.Join(commitartifact.ErrTransport, artifact.ErrInvalid), "artifact-invalid", false},
	} {
		t.Run(tc.code+"/"+tc.err.Error(), func(t *testing.T) {
			err := s.queueFailure(t.Context(), queue.Work{Attempts: 2}, tc.err)
			var f *queue.ExecutionFailure
			if !errors.As(err, &f) || f.Code != tc.code || f.Blocked == tc.retry || !errors.Is(err, tc.err) {
				t.Fatalf("classification: %+v", err)
			}
			deadline := now
			if tc.retry {
				deadline = now.Add(10 * time.Second)
			}
			if !f.RetryAt.Equal(deadline) {
				t.Fatal("wrong deadline", f.RetryAt)
			}
		})
	}
}

func TestQueueFailurePreservesCancellationAndUncertainWrites(t *testing.T) {
	s := Service{}
	for _, err := range []error{nil, context.Canceled, queue.ErrUncertain, errors.Join(queue.ErrUncertain, commitartifact.ErrTransport)} {
		if got := s.queueFailure(t.Context(), queue.Work{}, err); got != err {
			t.Fatal("reclassified uncertain/canceled operation", got)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := s.queueFailure(ctx, queue.Work{}, commitartifact.ErrTransport); got != commitartifact.ErrTransport {
		t.Fatal(got)
	}
	ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	if got := s.queueFailure(ctx, queue.Work{}, context.DeadlineExceeded); got != context.DeadlineExceeded {
		t.Fatal(got)
	}
}
