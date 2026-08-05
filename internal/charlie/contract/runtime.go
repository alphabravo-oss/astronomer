package contract

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxBridgeResponseBytes = 2 << 20
	maxBridgeEventBytes    = 256 << 10
	defaultCallTimeout     = 30 * time.Second
	defaultTurnTimeout     = 5 * time.Minute
)

// StableError is the complete error vocabulary exposed outside the bridge
// package. It intentionally contains neither upstream bodies nor certificate,
// agent, or Charlie-central addresses.
type StableError struct {
	Code       string
	Retryable  bool
	StatusCode int
}

func (e *StableError) Error() string { return "Charlie bridge request failed: " + e.Code }

type circuitState struct {
	mu          sync.Mutex
	failures    int
	openedUntil time.Time
}

// Runtime wraps the generated client with the runtime-only activation check,
// bounded I/O, content-safe errors, and a local circuit breaker. The circuit
// degrades only Charlie operations and is never part of Astronomer's core
// readiness.
type Runtime struct {
	client      *Client
	active      func() bool
	now         func() time.Time
	turnTimeout time.Duration
	state       circuitState
}

// NewRuntimeWithClientTLS constructs the sole runtime path to the local
// product agent. The active callback is re-read before every request and retry.
func NewRuntimeWithClientTLS(availability RuntimeAvailability, namespace, expectedServerIdentity string, tlsConfig *tls.Config, active func() bool) (*Runtime, error) {
	client, err := NewRuntimeClient(availability, namespace, expectedServerIdentity, tlsConfig)
	if err != nil {
		return nil, err
	}
	if active == nil || !active() {
		client.CloseIdleConnections()
		return nil, fmt.Errorf("Charlie runtime is inactive")
	}
	return &Runtime{client: client, active: active, now: time.Now, turnTimeout: defaultTurnTimeout}, nil
}

// NewConfigurationRuntimeWithClientTLS constructs the same bounded local-only
// transport for health, activation, and mode configuration while runtime
// authority is disabled. The caller-supplied predicate must remain tied to an
// installed product-local connection; it is re-read before every retry.
func NewConfigurationRuntimeWithClientTLS(availability FeatureAvailability, namespace, expectedServerIdentity string, tlsConfig *tls.Config, configured func() bool) (*Runtime, error) {
	client, err := NewLocalClient(availability, namespace, expectedServerIdentity, tlsConfig)
	if err != nil {
		return nil, err
	}
	if configured == nil || !configured() {
		client.CloseIdleConnections()
		return nil, fmt.Errorf("Charlie configuration transport is inactive")
	}
	return &Runtime{client: client, active: configured, now: time.Now, turnTimeout: defaultTurnTimeout}, nil
}

func (r *Runtime) Close() {
	if r != nil && r.client != nil {
		r.client.CloseIdleConnections()
	}
}

func (r *Runtime) allowed() error {
	if r == nil || r.client == nil || r.active == nil || !r.active() {
		return &StableError{Code: "integration_inactive"}
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if r.now().Before(r.state.openedUntil) {
		return &StableError{Code: "bridge_circuit_open", Retryable: true}
	}
	return nil
}

func (r *Runtime) record(err error) {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if err == nil {
		r.state.failures = 0
		r.state.openedUntil = time.Time{}
		return
	}
	r.state.failures++
	if r.state.failures >= 3 {
		r.state.openedUntil = r.now().Add(30 * time.Second)
	}
}

// DoJSON performs one bounded bridge request. Mutating requests require a
// stable idempotency key. Only GET/HEAD or keyed requests are retried.
func (r *Runtime) DoJSON(ctx context.Context, method, path, idempotencyKey string, input, output any) error {
	return r.doJSONWithAuthorization(ctx, method, path, idempotencyKey, "", input, output)
}

// DoJSONAuthorized performs a scoped bridge request with the short-lived,
// product-issued opaque authorization reference required by the session,
// approval, investigation, and finding surfaces.
func (r *Runtime) DoJSONAuthorized(ctx context.Context, method, path, idempotencyKey, authorizationRef string, input, output any) error {
	if !validAuthorizationRef(authorizationRef) {
		return &StableError{Code: "authorization_ref_required"}
	}
	return r.doJSONWithAuthorization(ctx, method, path, idempotencyKey, authorizationRef, input, output)
}

func (r *Runtime) doJSONWithAuthorization(ctx context.Context, method, path, idempotencyKey, authorizationRef string, input, output any) error {
	if err := r.allowed(); err != nil {
		return err
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if !safeBridgePath(path) {
		return &StableError{Code: "invalid_bridge_path"}
	}
	if requiresAuthorization(path) && !validAuthorizationRef(authorizationRef) {
		return &StableError{Code: "authorization_ref_required"}
	}
	idempotent := method == http.MethodGet || method == http.MethodHead
	if !idempotent && strings.TrimSpace(idempotencyKey) == "" {
		return &StableError{Code: "idempotency_key_required"}
	}
	var encoded []byte
	var err error
	if input != nil {
		encoded, err = json.Marshal(input)
		if err != nil || len(encoded) > maxBridgeResponseBytes {
			return &StableError{Code: "invalid_request"}
		}
	}
	attempts := 1
	if idempotent || idempotencyKey != "" {
		attempts = 3
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if err = r.allowed(); err != nil {
			return err
		}
		callCtx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
		err = r.doJSON(callCtx, method, path, idempotencyKey, authorizationRef, encoded, output)
		cancel()
		if err == nil {
			r.record(nil)
			return nil
		}
		var stable *StableError
		if !errors.As(err, &stable) || !stable.Retryable || attempt+1 == attempts {
			break
		}
		select {
		case <-ctx.Done():
			return &StableError{Code: "request_cancelled"}
		case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
		}
	}
	r.record(err)
	return err
}

func requiresAuthorization(path string) bool {
	for _, prefix := range []string{"/sessions", "/approvals", "/investigations", "/findings"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func (r *Runtime) doJSON(ctx context.Context, method, path, key, authorizationRef string, encoded []byte, output any) error {
	request, err := http.NewRequestWithContext(ctx, method, r.client.endpoint+path, bytes.NewReader(encoded))
	if err != nil {
		return &StableError{Code: "invalid_request"}
	}
	request.Header.Set("Accept", "application/json")
	if encoded != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	if authorizationRef != "" {
		request.Header.Set("X-Charlie-Authorization-Ref", authorizationRef)
	}
	response, err := r.client.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return &StableError{Code: "bridge_timeout", Retryable: true}
		}
		return &StableError{Code: "bridge_unavailable", Retryable: true}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxBridgeResponseBytes+1))
		return mapStatus(response.StatusCode)
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBridgeResponseBytes+1))
	if err != nil || len(body) > maxBridgeResponseBytes {
		return &StableError{Code: "invalid_bridge_response"}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return &StableError{Code: "invalid_bridge_response"}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return &StableError{Code: "invalid_bridge_response"}
	}
	return nil
}

func validAuthorizationRef(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256 && !strings.ContainsAny(value, "\r\n\x00")
}

func safeBridgePath(path string) bool {
	parsed, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.RawQuery == "" {
		return true
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return false
	}
	for name, values := range query {
		if len(values) != 1 || (name != "cursor" && name != "limit") {
			return false
		}
		if name == "cursor" && (values[0] == "" || len(values[0]) > 128) {
			return false
		}
		if name == "limit" {
			limit, parseErr := strconv.Atoi(values[0])
			if parseErr != nil || limit < 1 || limit > 100 {
				return false
			}
		}
	}
	return true
}

func mapStatus(status int) error {
	switch status {
	case http.StatusUnauthorized:
		return &StableError{Code: "bridge_unauthenticated", StatusCode: status}
	case http.StatusForbidden:
		return &StableError{Code: "bridge_forbidden", StatusCode: status}
	case http.StatusConflict:
		return &StableError{Code: "bridge_conflict", StatusCode: status}
	case http.StatusTooManyRequests:
		return &StableError{Code: "bridge_rate_limited", StatusCode: status, Retryable: true}
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return &StableError{Code: "bridge_unavailable", StatusCode: status, Retryable: true}
	default:
		return &StableError{Code: "bridge_rejected", StatusCode: status}
	}
}

// Event preserves central event and turn identifiers exactly. Data is passed to
// the caller as opaque JSON and is never logged by this package.
type Event struct {
	ID    string
	Event string
	Data  []byte
	Retry time.Duration
}

// StreamEvents resumes with Last-Event-ID and acknowledges an event only after
// the callback returns nil. The caller persists that acknowledged ID.
func (r *Runtime) StreamEvents(ctx context.Context, sessionID, lastEventID string, handle func(Event) error) error {
	return r.StreamEventsAuthorized(ctx, sessionID, lastEventID, "", handle)
}

// StreamEventsAuthorized resumes an authorized session stream. The reference
// is re-sent on every reconnect, while the active callback is re-read before
// every connection attempt.
func (r *Runtime) StreamEventsAuthorized(ctx context.Context, sessionID, lastEventID, authorizationRef string, handle func(Event) error) error {
	if strings.TrimSpace(sessionID) == "" || handle == nil {
		return &StableError{Code: "invalid_request"}
	}
	if !validAuthorizationRef(authorizationRef) {
		return &StableError{Code: "authorization_ref_required"}
	}
	path := "/sessions/" + url.PathEscape(sessionID) + "/events"
	acknowledged := lastEventID
	for reconnect := 0; ; reconnect++ {
		if err := r.allowed(); err != nil {
			return err
		}
		turnTimeout := r.turnTimeout
		if turnTimeout <= 0 {
			turnTimeout = defaultTurnTimeout
		}
		turnCtx, cancelTurn := context.WithTimeout(ctx, turnTimeout)
		request, err := http.NewRequestWithContext(turnCtx, http.MethodGet, r.client.endpoint+path, nil)
		if err != nil {
			cancelTurn()
			return &StableError{Code: "invalid_request"}
		}
		request.Header.Set("Accept", "text/event-stream")
		if acknowledged != "" {
			request.Header.Set("Last-Event-ID", acknowledged)
		}
		request.Header.Set("X-Charlie-Authorization-Ref", authorizationRef)
		response, err := r.client.httpClient.Do(request)
		if err == nil && response.StatusCode == http.StatusOK {
			err = consumeSSE(response.Body, func(event Event) error {
				if event.ID != "" && event.ID == acknowledged {
					return nil
				}
				if callbackErr := handle(event); callbackErr != nil {
					return callbackErr
				}
				if event.ID != "" {
					acknowledged = event.ID
				}
				return nil
			})
			_ = response.Body.Close()
		} else if err == nil {
			_ = response.Body.Close()
			err = mapStatus(response.StatusCode)
		} else {
			err = &StableError{Code: "bridge_unavailable", Retryable: true}
		}
		turnExpired := errors.Is(turnCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
		cancelTurn()
		if turnExpired {
			err = &StableError{Code: "bridge_turn_timeout", Retryable: true}
		}
		if ctx.Err() != nil {
			return nil
		}
		var stable *StableError
		if err != nil && (!errors.As(err, &stable) || !stable.Retryable) {
			return err
		}
		if reconnect >= 20 {
			r.record(err)
			return &StableError{Code: "bridge_stream_unavailable", Retryable: true}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(min(reconnect+1, 10)) * 100 * time.Millisecond):
		}
	}
}

func consumeSSE(reader io.Reader, handle func(Event) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxBridgeEventBytes)
	current := Event{}
	data := make([]string, 0, 4)
	dispatch := func() error {
		if len(data) == 0 && current.ID == "" && current.Event == "" {
			return nil
		}
		current.Data = []byte(strings.Join(data, "\n"))
		if len(current.Data) > maxBridgeEventBytes {
			return &StableError{Code: "bridge_event_too_large"}
		}
		if err := handle(current); err != nil {
			return err
		}
		current = Event{}
		data = data[:0]
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if found {
			value = strings.TrimPrefix(value, " ")
		}
		switch name {
		case "id":
			if !strings.ContainsRune(value, '\x00') {
				current.ID = value
			}
		case "event":
			current.Event = value
		case "data":
			data = append(data, value)
		case "retry":
			var milliseconds int64
			if _, err := fmt.Sscan(value, &milliseconds); err == nil && milliseconds >= 0 && milliseconds <= 30_000 {
				current.Retry = time.Duration(milliseconds) * time.Millisecond
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return &StableError{Code: "bridge_stream_unavailable", Retryable: true}
	}
	return dispatch()
}
