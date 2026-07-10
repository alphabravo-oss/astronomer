package sqlc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// This is an opt-in real PostgreSQL test because the unit-test environment has
// no database. CI/release rehearsals set DEX_CONCURRENCY_TEST_DATABASE_URL to a
// disposable database; the test creates and drops an isolated schema.
func TestDexLifecycleAdvisoryLockRejectsStaleEnableAndRestore(t *testing.T) {
	dsn := os.Getenv("DEX_CONCURRENCY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("DEX_CONCURRENCY_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c1, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close(ctx)
	c2, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close(ctx)
	schema := "dex_concurrency_" + uuid.NewString()[:8]
	if _, err = c1.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, schema)); err != nil {
		t.Fatal(err)
	}
	defer c1.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
	ddl := fmt.Sprintf(`SET search_path TO %s;
CREATE TABLE dex_settings(id uuid PRIMARY KEY,issuer_url text NOT NULL DEFAULT '',cluster_id uuid,namespace text NOT NULL DEFAULT 'dex',release_name text NOT NULL DEFAULT 'dex',configmap_name text NOT NULL DEFAULT 'runtime',public_clients jsonb NOT NULL DEFAULT '[]',expiry jsonb NOT NULL DEFAULT '{}',extra jsonb NOT NULL DEFAULT '{}',created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),runtime_secret_name text NOT NULL DEFAULT 'runtime',public_clients_encrypted text NOT NULL DEFAULT '',public_clients_cutover_at timestamptz,chart_release_name text NOT NULL DEFAULT '',deployment_name text NOT NULL DEFAULT 'dex',service_name text NOT NULL DEFAULT 'dex',runtime_generation bigint NOT NULL DEFAULT 1,runtime_applied_generation bigint NOT NULL DEFAULT 1,runtime_phase text NOT NULL DEFAULT 'fresh',runtime_staged_generation bigint NOT NULL DEFAULT 1,saga_previous_sso_enabled boolean NOT NULL DEFAULT true);
CREATE TABLE sso_configurations(id uuid PRIMARY KEY DEFAULT '00000000-0000-0000-0000-000000000002',provider text UNIQUE NOT NULL,is_enabled boolean NOT NULL DEFAULT false,display_name text NOT NULL DEFAULT '',config jsonb NOT NULL DEFAULT '{}',client_id text NOT NULL DEFAULT '',client_secret_encrypted text NOT NULL DEFAULT '',allowed_organizations jsonb NOT NULL DEFAULT '[]',allowed_domains jsonb NOT NULL DEFAULT '[]',auto_create_users boolean NOT NULL DEFAULT true,default_global_role_id uuid,created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),migrated_to_dex_at timestamptz);
CREATE TABLE dex_connectors(id uuid PRIMARY KEY DEFAULT '00000000-0000-0000-0000-000000000003',name text UNIQUE NOT NULL,type text NOT NULL,display_name text NOT NULL DEFAULT '',config jsonb NOT NULL DEFAULT '{}',enabled boolean NOT NULL DEFAULT true,created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now());
INSERT INTO dex_settings(id) VALUES('00000000-0000-0000-0000-000000000001'); INSERT INTO sso_configurations(provider) VALUES('dex');`, schema)
	if _, err = c1.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	if _, err = c2.Exec(ctx, fmt.Sprintf(`SET search_path TO %s`, schema)); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"enable", "restore"} {
		t.Run(name, func(t *testing.T) {
			_, err := c1.Exec(ctx, `UPDATE dex_settings SET runtime_generation=1,runtime_applied_generation=1,runtime_staged_generation=1,runtime_phase='fresh',saga_previous_sso_enabled=true; UPDATE sso_configurations SET is_enabled=false`)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := c1.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(742193440558879931)`); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				if name == "enable" {
					_, e := New(c2).EnableDexSSOForGeneration(ctx, EnableDexSSOForGenerationParams{RuntimeGeneration: 1, DisplayName: "Dex", Config: []byte(`{}`), ClientID: "old"})
					done <- e
				} else {
					_, e := New(c2).RestoreDexSSOForGeneration(ctx, RestoreDexSSOForGenerationParams{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), RuntimeGeneration: 1})
					done <- e
				}
			}()
			select {
			case e := <-done:
				t.Fatalf("%s did not block on advisory lock: %v", name, e)
			case <-time.After(150 * time.Millisecond):
			}
			generation, stageErr := New(tx).StageDexSettingsAndDisableSSO(ctx, StageDexSettingsAndDisableSSOParams{
				ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), IssuerUrl: "https://dex.example.com",
				Namespace: "dex", ReleaseName: "dex", ConfigmapName: "runtime", RuntimeSecretName: "runtime",
				PublicClients: []byte(`[]`), Expiry: []byte(`{}`), Extra: []byte(`{}`),
				DeploymentName: "dex", ServiceName: "dex", RuntimePhase: "fresh",
			})
			if stageErr != nil || generation != 2 {
				t.Fatalf("stage N+1 generation=%d err=%v", generation, stageErr)
			}
			if _, err = tx.Exec(ctx, `UPDATE dex_settings SET runtime_applied_generation=1,runtime_staged_generation=1`); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if e := <-done; !errors.Is(e, pgx.ErrNoRows) {
				t.Fatalf("stale %s error=%v", name, e)
			}
			var enabled bool
			if err = c1.QueryRow(ctx, `SELECT is_enabled FROM sso_configurations WHERE provider='dex'`).Scan(&enabled); err != nil || enabled {
				t.Fatalf("stale %s ended enabled=%v err=%v", name, enabled, err)
			}
		})
	}
	t.Run("connector failure is atomic", func(t *testing.T) {
		if _, err := c1.Exec(ctx, `DELETE FROM dex_settings; DELETE FROM dex_connectors`); err != nil {
			t.Fatal(err)
		}
		_, err := New(c1).StageCreateDexConnector(ctx, StageCreateDexConnectorParams{Name: "must-not-persist", Type: "saml", Config: []byte(`{}`), Enabled: true})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("create without settings err=%v", err)
		}
		var count int
		if err = c1.QueryRow(ctx, `SELECT count(*) FROM dex_connectors`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed mutation persisted count=%d err=%v", count, err)
		}
	})
}
