package operationstate

import "testing"

func TestOperationStatePredicates(t *testing.T) {
	for _, status := range []string{Pending, Running} {
		if !IsActive(status) {
			t.Fatalf("status %q should be active", status)
		}
	}
	for _, status := range []string{Failed, Superseded} {
		if !IsRetryable(status) || !IsFailure(status) {
			t.Fatalf("status %q should be retryable failure", status)
		}
	}
	for _, status := range []string{Pending, Running, Completed} {
		if IsRetryable(status) {
			t.Fatalf("status %q should not be retryable", status)
		}
	}
}

func TestQueueDepth(t *testing.T) {
	got := QueueDepth(map[string]int{
		Pending:    2,
		Running:    3,
		Completed:  5,
		Superseded: 7,
	})
	if got != 5 {
		t.Fatalf("queue depth = %d, want 5", got)
	}
}
