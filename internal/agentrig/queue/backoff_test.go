package queue

import (
	"errors"
	"testing"
	"time"
)

func TestRetryFailureBudget(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	cause := errors.New("private diagnostic")
	for attempt, seconds := range []int{5, 5, 10, 20, 30, 30, 30, 30, 0} {
		f := RetryFailure(now, uint64(attempt), "transport", cause)
		if f.Blocked != (attempt == 8) || !f.RetryAt.Equal(now.Add(time.Duration(seconds)*time.Second)) || f.Code != "transport" || !errors.Is(f, cause) {
			t.Fatalf("attempt %d: %+v", attempt, f)
		}
	}
	if f := RetryFailure(now, ^uint64(0), "transport", cause); !f.Blocked || !f.RetryAt.Equal(now) {
		t.Fatal("attempt overflow", f)
	}
}
