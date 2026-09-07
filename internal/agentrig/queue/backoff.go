package queue

import "time"

// RetryLimit bounds automatic attempts for a sealed batch. Attempts counts worker
// claims, including interrupted claims; it is never reset by reaching a new phase.
const RetryLimit uint64 = 8

// RetryFailure schedules a classified retry after 5s, 10s, 20s, then 30s
// per claim, or blocks at RetryLimit. It does not sleep, discard work or persist
// Cause. Vendors choose which failures are retryable and supply a fixed safe code.
// now must be nonzero. A zero attempt is treated as the first claim.
func RetryFailure(now time.Time, attempt uint64, code string, cause error) *ExecutionFailure {
	failure := &ExecutionFailure{Code: code, RetryAt: now, Cause: cause}
	if attempt >= RetryLimit {
		failure.Blocked = true
		return failure
	}
	delay := 5 * time.Second
	for n := uint64(1); n < attempt && delay < 30*time.Second; n++ {
		delay = min(2*delay, 30*time.Second)
	}
	failure.RetryAt = now.Add(delay)
	return failure
}
