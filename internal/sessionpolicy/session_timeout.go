package sessionpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

type contextKey struct{}

// SettingKey is the platform setting consulted for every interactive
// access-token mint. The setting controls an absolute JWT TTL; it is not an
// idle or sliding session timeout.
const SettingKey = "session.timeout_minutes"

const (
	// DefaultMinutes is the single fallback used by boot config, the
	// platform-settings registry, and runtime JWT minting.
	DefaultMinutes = 60
	MinMinutes     = 5
	MaxMinutes     = 10080
)

// ParseMinutes decodes the stored JSON setting. Missing/null data resolves to
// the canonical default. Corrupt or out-of-range data also returns the safe
// default together with an error so callers can emit an actionable operational
// log without ever minting an unbounded token.
func ParseMinutes(raw json.RawMessage) (int, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return DefaultMinutes, nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return DefaultMinutes, fmt.Errorf("decode integer: %w", err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return DefaultMinutes, err
	}
	number, ok := value.(json.Number)
	if !ok {
		return DefaultMinutes, fmt.Errorf("value must be a JSON integer")
	}
	minutes, err := number.Int64()
	if err != nil {
		return DefaultMinutes, fmt.Errorf("value must be an integer: %w", err)
	}
	if minutes < MinMinutes || minutes > MaxMinutes {
		return DefaultMinutes, fmt.Errorf("value must be between %d and %d minutes", MinMinutes, MaxMinutes)
	}
	return int(minutes), nil
}

// WithMinutes carries an already-resolved policy through a single request.
// Password login and refresh resolve the AuthHandler policy once, then the JWT
// provider consumes this value instead of issuing a second database read.
func WithMinutes(ctx context.Context, minutes int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if minutes < MinMinutes || minutes > MaxMinutes {
		minutes = DefaultMinutes
	}
	return context.WithValue(ctx, contextKey{}, minutes)
}

// MinutesFromContext returns a policy previously attached by WithMinutes.
func MinutesFromContext(ctx context.Context) (int, bool) {
	if ctx == nil {
		return 0, false
	}
	minutes, ok := ctx.Value(contextKey{}).(int)
	return minutes, ok
}

func ensureJSONEOF(dec *json.Decoder) error {
	var trailing any
	if err := dec.Decode(&trailing); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode trailing data: %w", err)
	}
	return fmt.Errorf("value must contain exactly one JSON integer")
}
