//go:build integration

package db_test

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/alphabravocompany/astronomer-go/internal/db/migrations"
	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

func TestIdentityRetirementPreservesLocalSecurityAndRoles(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	database, err := sql.Open("pgx", testdb.EmptyDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)
	provider, err := goose.NewProvider(goose.DialectPostgres, database, migrations.FS(), goose.WithTableName(migrations.VersionTable))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 86); err != nil {
		t.Fatal(err)
	}
	_, err = database.ExecContext(ctx, `SET search_path=public;
 INSERT INTO users(id,email,username,password,is_superuser) VALUES ('11111111-1111-4111-8111-111111111111','retirement@example.test','retirement','existing-password-hash',true);
 INSERT INTO users(id,email,username,password) VALUES ('44444444-4444-4444-8444-444444444444','broker@example.test','broker','retained-broker-local-password');
 INSERT INTO sso_sessions(jti,user_id,provider_name,expires_at) VALUES ('legacy-broker-session','44444444-4444-4444-8444-444444444444','dex',now()+interval '1 day');
 INSERT INTO refresh_session_families(family_hash,user_id,expires_at) VALUES (decode(repeat('a',64),'hex'),'44444444-4444-4444-8444-444444444444',now()+interval '1 day');
 INSERT INTO dex_connectors(id,name,type) VALUES ('22222222-2222-4222-8222-222222222222','legacy','oidc');
 INSERT INTO global_roles(id,name) VALUES ('33333333-3333-4333-8333-333333333333','retirement-role');
 INSERT INTO global_role_bindings(user_id,role_id,source,group_sync_connector_id) VALUES ('11111111-1111-4111-8111-111111111111','33333333-3333-4333-8333-333333333333','group_sync','22222222-2222-4222-8222-222222222222');
 INSERT INTO user_totp_enrollments(user_id,secret_key_id,secret_algorithm,secret_ciphertext,confirmed_at) VALUES ('11111111-1111-4111-8111-111111111111','test-key','age-v1',decode('abcd','hex'),now());
 INSERT INTO user_totp_recovery_codes(user_id,code_hash) VALUES ('11111111-1111-4111-8111-111111111111',repeat('a',64));`)
	if err != nil {
		t.Fatal(err)
	}
	const snapshot = `SELECT jsonb_build_object('users',(SELECT jsonb_agg(to_jsonb(t)) FROM users t WHERE username='retirement'),'roles',(SELECT jsonb_agg(to_jsonb(t)) FROM global_role_bindings t),'mfa',(SELECT jsonb_agg(to_jsonb(t)) FROM user_totp_enrollments t),'recovery',(SELECT jsonb_agg(to_jsonb(t)) FROM user_totp_recovery_codes t))::text`
	var before, after string
	if err = database.QueryRowContext(ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 87); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRowContext(ctx, snapshot).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("retirement changed existing local accounts, roles, MFA or recovery credentials")
	}
	var revoked bool
	if err = database.QueryRowContext(ctx, `SELECT (SELECT tokens_invalidated_at IS NOT NULL AND password='retained-broker-local-password' FROM users WHERE username='broker') AND (SELECT revoked_at IS NOT NULL AND revoke_reason='identity_broker_retired' FROM refresh_session_families WHERE user_id='44444444-4444-4444-8444-444444444444') AND EXISTS(SELECT 1 FROM jwt_revocations WHERE jti='legacy-broker-session')`).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("old broker session remains usable or credentials changed: %v", err)
	}

	var retired bool
	if err = database.QueryRowContext(ctx, `SELECT to_regclass('public.dex_connectors') IS NULL AND to_regclass('public.dex_settings') IS NULL AND to_regclass('public.dex_operations') IS NULL AND to_regclass('public.external_principals') IS NULL`).Scan(&retired); err != nil || !retired {
		t.Fatalf("legacy broker tables remain: %v", err)
	}
	var links int
	if err = database.QueryRowContext(ctx, `SELECT count(*) FROM shared_identity_bindings`).Scan(&links); err != nil || links != 0 {
		t.Fatalf("old subjects must never be inferred at a new issuer: %v", err)
	}
	if _, err = provider.UpTo(ctx, 88); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRowContext(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("provisioning journal migration changed local security records")
	}
	var operations int
	if err = database.QueryRowContext(ctx, `SELECT count(*) FROM identity_provisioning_operations`).Scan(&operations); err != nil || operations != 0 {
		t.Fatal("journal migration invented provisioning operations")
	}
}
