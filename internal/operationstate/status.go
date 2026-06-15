package operationstate

const (
	Pending    = "pending"
	Running    = "running"
	Completed  = "completed"
	Failed     = "failed"
	Superseded = "superseded"
)

func IsActive(status string) bool {
	return status == Pending || status == Running
}

func IsRetryable(status string) bool {
	return status == Failed || status == Superseded
}

func IsFailure(status string) bool {
	return status == Failed || status == Superseded
}

func QueueDepth(counts map[string]int) int {
	if counts == nil {
		return 0
	}
	return counts[Pending] + counts[Running]
}
