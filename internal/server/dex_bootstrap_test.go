package server

// Tests for migration 045's dex_settings auto-bootstrap.
//
// Three scenarios mirror the spec from the FEATURE doc:
//   1. bundle enabled + empty dex_settings    → row seeded
//   2. bundle enabled + row exists            → no change (idempotent)
//   3. bundle disabled                         → no change
//
// Plus a fourth covering the deprecation warning path that fires when
// the boot path sees enabled-but-unmigrated sso_configurations rows.

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeDexBootstrapQuerier records the calls the bootstrap makes against a DB
// stub and serves the next Get / Upsert result in line. The fake doesn't try
// to be a fully-faithful DB — only the surface the bootstrap touches.
type fakeDexBootstrapQuerier struct {
	getResult       sqlc.DexSetting
	getErr          error
	upsertCalls     int
	upsertResult    sqlc.DexSetting
	upsertErr       error
	upsertLastParam sqlc.UpsertDexSettingsParams
}

func (f *fakeDexBootstrapQuerier) GetDexSettings(_ context.Context, id uuid.UUID) (sqlc.DexSetting, error) {
	if f.getErr != nil {
		return sqlc.DexSetting{}, f.getErr
	}
	if f.getResult.ID != id {
		// Match production: GetDexSettings returns pgx.ErrNoRows when the
		// singleton row hasn't been written yet.
		return sqlc.DexSetting{}, pgx.ErrNoRows
	}
	return f.getResult, nil
}

func (f *fakeDexBootstrapQuerier) UpsertDexSettings(_ context.Context, arg sqlc.UpsertDexSettingsParams) (sqlc.DexSetting, error) {
	f.upsertCalls++
	f.upsertLastParam = arg
	if f.upsertErr != nil {
		return sqlc.DexSetting{}, f.upsertErr
	}
	if f.upsertResult.ID == uuid.Nil {
		// Synthesize a return value the bootstrap doesn't actually consume.
		return sqlc.DexSetting{
			ID:            arg.ID,
			IssuerUrl:     arg.IssuerUrl,
			Namespace:     arg.Namespace,
			ReleaseName:   arg.ReleaseName,
			ConfigmapName: arg.ConfigmapName,
		}, nil
	}
	return f.upsertResult, nil
}

// envMap is a test substitute for os.LookupEnv that draws from a fixed table.
type envMap map[string]string

func (e envMap) lookup(key string) (string, bool) {
	v, ok := e[key]
	return v, ok
}

func TestDexBootstrap_SeedsSettingsWhenBundled(t *testing.T) {
	q := &fakeDexBootstrapQuerier{}
	env := envMap{
		"DEX_BUNDLED_ENABLED":        "true",
		"DEX_BUNDLED_NAMESPACE":      "astronomer",
		"DEX_BUNDLED_RELEASE_NAME":   "astronomer-dex",
		"DEX_BUNDLED_CONFIGMAP_NAME": "astronomer-dex-config",
		"DEX_BUNDLED_ISSUER_URL":     "https://astronomer.example.com/dex",
	}
	seeded, err := seedBundledDexSettings(context.Background(), q, slog.Default(), env.lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !seeded {
		t.Fatalf("expected seeded=true on first-boot bundle-enabled case")
	}
	if q.upsertCalls != 1 {
		t.Fatalf("expected exactly one UpsertDexSettings call, got %d", q.upsertCalls)
	}
	if got := q.upsertLastParam.IssuerUrl; got != "https://astronomer.example.com/dex" {
		t.Errorf("issuer mismatch: got %q", got)
	}
	if got := q.upsertLastParam.Namespace; got != "astronomer" {
		t.Errorf("namespace mismatch: got %q", got)
	}
	if got := q.upsertLastParam.ReleaseName; got != "astronomer-dex" {
		t.Errorf("release_name mismatch: got %q", got)
	}
	if got := q.upsertLastParam.ConfigmapName; got != "astronomer-dex-config" {
		t.Errorf("configmap_name mismatch: got %q", got)
	}
	if q.upsertLastParam.ID != dexBootstrapSingletonID {
		t.Errorf("singleton id mismatch: got %v", q.upsertLastParam.ID)
	}
}

func TestDexBootstrap_NoOpWhenSettingsExist(t *testing.T) {
	q := &fakeDexBootstrapQuerier{
		getResult: sqlc.DexSetting{
			ID:        dexBootstrapSingletonID,
			IssuerUrl: "https://operator-managed.example.com/dex",
			Namespace: "auth",
		},
	}
	env := envMap{
		"DEX_BUNDLED_ENABLED":    "true",
		"DEX_BUNDLED_ISSUER_URL": "https://astronomer.example.com/dex",
	}
	seeded, err := seedBundledDexSettings(context.Background(), q, slog.Default(), env.lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seeded {
		t.Fatalf("expected seeded=false when settings already exist")
	}
	if q.upsertCalls != 0 {
		t.Fatalf("expected no UpsertDexSettings calls; got %d", q.upsertCalls)
	}
}

func TestDexBootstrap_NoOpWhenBundledDisabled(t *testing.T) {
	q := &fakeDexBootstrapQuerier{}
	// Two flavours of "disabled": env unset, and env set to false.
	for _, env := range []envMap{
		{},
		{"DEX_BUNDLED_ENABLED": "false"},
		{"DEX_BUNDLED_ENABLED": "0"},
	} {
		seeded, err := seedBundledDexSettings(context.Background(), q, slog.Default(), env.lookup)
		if err != nil {
			t.Fatalf("env=%v: unexpected error: %v", env, err)
		}
		if seeded {
			t.Fatalf("env=%v: expected seeded=false", env)
		}
		if q.upsertCalls != 0 {
			t.Fatalf("env=%v: expected no UpsertDexSettings calls; got %d", env, q.upsertCalls)
		}
	}
}

func TestDexBootstrap_NoOpWhenIssuerEmpty(t *testing.T) {
	q := &fakeDexBootstrapQuerier{}
	env := envMap{"DEX_BUNDLED_ENABLED": "true"} // DEX_BUNDLED_ISSUER_URL deliberately omitted
	seeded, err := seedBundledDexSettings(context.Background(), q, slog.Default(), env.lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seeded {
		t.Fatalf("expected seeded=false when issuer URL is missing")
	}
	if q.upsertCalls != 0 {
		t.Fatalf("expected no UpsertDexSettings calls; got %d", q.upsertCalls)
	}
}

func TestDexBootstrap_UsesFallbackDefaultsForMissingEnv(t *testing.T) {
	q := &fakeDexBootstrapQuerier{}
	env := envMap{
		"DEX_BUNDLED_ENABLED":    "true",
		"DEX_BUNDLED_ISSUER_URL": "https://astronomer.example.com/dex/",
		// Intentionally no namespace/release/configmap — fallback defaults must kick in.
	}
	seeded, err := seedBundledDexSettings(context.Background(), q, slog.Default(), env.lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !seeded {
		t.Fatalf("expected seed even with missing optional env vars")
	}
	if q.upsertLastParam.Namespace != "astronomer" {
		t.Errorf("expected default namespace 'astronomer'; got %q", q.upsertLastParam.Namespace)
	}
	if q.upsertLastParam.ReleaseName != "astronomer-dex" {
		t.Errorf("expected default release 'astronomer-dex'; got %q", q.upsertLastParam.ReleaseName)
	}
	if q.upsertLastParam.ConfigmapName != "astronomer-dex-config" {
		t.Errorf("expected default configmap 'astronomer-dex-config'; got %q", q.upsertLastParam.ConfigmapName)
	}
	// Trailing slash on the issuer URL must be trimmed.
	if q.upsertLastParam.IssuerUrl != "https://astronomer.example.com/dex" {
		t.Errorf("expected trailing slash trim; got %q", q.upsertLastParam.IssuerUrl)
	}
}

// fakeLegacySSOQuerier serves the boot-time count query.
type fakeLegacySSOQuerier struct {
	count int64
	err   error
	calls int
}

func (f *fakeLegacySSOQuerier) CountActiveUnmigratedSSORows(_ context.Context) (int64, error) {
	f.calls++
	return f.count, f.err
}

func TestStartupWarning_LegacySSORows(t *testing.T) {
	cases := []struct {
		name    string
		count   int64
		wantErr bool
	}{
		{"no rows -> no warning emitted", 0, false},
		{"one row -> warning emitted", 1, false},
		{"many rows -> warning emitted", 5, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := &fakeLegacySSOQuerier{count: tc.count}
			err := WarnIfLegacySSORowsActive(context.Background(), q, slog.Default())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if q.calls != 1 {
				t.Fatalf("expected exactly one count query; got %d", q.calls)
			}
		})
	}
}

func TestStartupWarning_LegacySSO_ErrorPropagated(t *testing.T) {
	wanted := errors.New("db gone")
	q := &fakeLegacySSOQuerier{err: wanted}
	err := WarnIfLegacySSORowsActive(context.Background(), q, slog.Default())
	if !errors.Is(err, wanted) {
		t.Fatalf("expected wrapped db error; got %v", err)
	}
}

func TestStartupWarning_NilQuerierIsSafe(t *testing.T) {
	if err := WarnIfLegacySSORowsActive(context.Background(), nil, slog.Default()); err != nil {
		t.Fatalf("nil querier should be a no-op; got %v", err)
	}
}
