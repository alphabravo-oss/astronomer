package providers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxProviderErrorBody = 64 << 10

// UnsupportedEnforcementError is permanent and must never be retried as a
// transient cloud failure.
type UnsupportedEnforcementError struct {
	Provider ProviderID
	Reason   string
}

func (e *UnsupportedEnforcementError) Error() string {
	return fmt.Sprintf("provider %s cannot enforce API-server allow-lists: %s", e.Provider, e.Reason)
}

// HTTPError retains the status and retry hint without leaking request headers
// or credentials into logs and persisted reconciliation state.
type HTTPError struct {
	Provider   ProviderID
	Operation  string
	StatusCode int
	Body       string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s %s failed with status %d: %s", e.Provider, e.Operation, e.StatusCode, e.Body)
}

func (e *HTTPError) Temporary() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= http.StatusInternalServerError
}

// IsAuthorizationError classifies provider failures that require operator
// credential/role repair rather than ordinary retry backoff. errors.As keeps
// the classification intact through driver and task wrapping.
func IsAuthorizationError(err error) bool {
	var providerErr *HTTPError
	return errors.As(err, &providerErr) && (providerErr.StatusCode == http.StatusUnauthorized || providerErr.StatusCode == http.StatusForbidden)
}

func retryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	if seconds, err := strconv.Atoi(header); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(header); err == nil {
		if delay := time.Until(at); delay > 0 {
			return delay
		}
	}
	return 0
}
