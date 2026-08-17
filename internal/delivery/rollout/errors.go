package rollout

import (
	"errors"
	"fmt"
)

// ErrorCode is stable across REST, worker, and audit adapters. Error messages
// contain no source, credential, or cluster diagnostic content.
type ErrorCode string

const (
	CodeInvalidInput        ErrorCode = "invalid_input"
	CodeIdempotencyConflict ErrorCode = "idempotency_conflict"
	CodePreviewStale        ErrorCode = "preview_stale"
	CodeTargetChanged       ErrorCode = "target_changed"
	CodeNoClusters          ErrorCode = "no_clusters"
	CodeInvalidCohorts      ErrorCode = "invalid_cohorts"
	CodeLeaseLost           ErrorCode = "lease_lost"
	CodeStaleFence          ErrorCode = "stale_fence"
	CodeInvariant           ErrorCode = "invariant_violation"
)

type Error struct {
	Code  ErrorCode
	Field string
	Cause error
}

func (e *Error) Error() string {
	if e == nil {
		return "rollout operation failed"
	}
	if e.Field != "" {
		return fmt.Sprintf("rollout %s (%s): %v", e.Code, e.Field, e.Cause)
	}
	return fmt.Sprintf("rollout %s: %v", e.Code, e.Cause)
}

func (e *Error) Unwrap() error { return e.Cause }

func HasCode(err error, code ErrorCode) bool {
	var rolloutError *Error
	return errors.As(err, &rolloutError) && rolloutError.Code == code
}

func fail(code ErrorCode, field, message string) *Error {
	return &Error{Code: code, Field: field, Cause: errors.New(message)}
}
