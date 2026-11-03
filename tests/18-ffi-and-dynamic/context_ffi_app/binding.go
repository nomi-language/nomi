package contextffiapp

import (
	"context"
	"time"
)

// WaitForCancel blocks until the calling task's context ends, or until
// limitMs passes, and says which happened.
func WaitForCancel(ctx context.Context, limitMs int64) string {
	select {
	case <-ctx.Done():
		return ctx.Err().Error()
	case <-time.After(time.Duration(limitMs) * time.Millisecond):
		return "the context did not end"
	}
}

// HasDeadline reports whether the calling task's context has a deadline.
func HasDeadline(ctx context.Context) bool {
	_, ok := ctx.Deadline()
	return ok
}
