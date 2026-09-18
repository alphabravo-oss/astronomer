package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

type strictSettingReader struct {
	value string
	err   error
}

func (r *strictSettingReader) GetPlatformSetting(context.Context, string) (sqlc.PlatformSetting, error) {
	return sqlc.PlatformSetting{Value: json.RawMessage(r.value)}, r.err
}

func TestSettingsCacheStrictReadDistinguishesDefaultFromFailure(t *testing.T) {
	reader := &strictSettingReader{err: pgx.ErrNoRows}
	cache := NewSettingsCache(reader, time.Minute)
	ctx := context.Background()
	if value, err := cache.StringValueWithError(ctx, "audit.read_tier", "standard"); err != nil || value != "standard" {
		t.Fatalf("missing: %q %v", value, err)
	}
	cache.Invalidate("audit.read_tier")
	reader.err = errors.New("database unavailable")
	if _, err := cache.StringValueWithError(ctx, "audit.read_tier", "standard"); err == nil {
		t.Fatal("database failure became a default")
	}
	reader.err, reader.value = nil, `"incident"`
	if value, err := cache.StringValueWithError(ctx, "audit.read_tier", "standard"); err != nil || value != "incident" {
		t.Fatalf("recovery: %q %v", value, err)
	}
	cache.Invalidate("audit.read_tier")
	reader.value = "123"
	if _, err := cache.StringValueWithError(ctx, "audit.read_tier", "standard"); err == nil {
		t.Fatal("malformed setting became a default")
	}
}
