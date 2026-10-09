package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

func seed(ctx context.Context, pool *pgxpool.Pool, keyPath, commit string) (manifest, error) {
	m := manifest{SchemaVersion: 1, Owner: "Plan032 W12", SourceCommit: commit, RunID: uuid.New(), Migration: 72, KeyIDs: []string{"w12-current", "w12-old"}, Limits: []string{
		"Synthetic credentials and production serialization/DB readers only; no customer key or real Charlie/provider upstream is contacted.",
		"Audit intent identity/order/data preservation is checked; the application does not implement a native cryptographic audit hash chain.",
		"Backup/restore and independent protected key-reference recovery are performed separately by the owned-cluster operator.",
	}}
	var existing int
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM clusters)+(SELECT count(*) FROM projects)+(SELECT count(*) FROM charlie_connections)").Scan(&existing); err != nil || existing != 0 {
		return m, errors.New("dedicated fixture database must have no existing domain rows")
	}
	k, keyDigest, err := createKeys(keyPath)
	if err != nil {
		return m, err
	}
	m.KeyRefDigest = keyDigest
	tx, err := pool.Begin(ctx)
	if err != nil {
		return m, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for i, key := range []string{"w12-old:" + k.Old, "w12-current:" + k.Current} {
		encryptor, err := auth.NewEncryptor(key)
		if err != nil {
			return m, err
		}
		ids := tenant{Cluster: uuid.New(), Project: uuid.New(), User: uuid.New(), Repository: uuid.New(), Connection: uuid.New(), Source: uuid.New(), Bundle: uuid.New(), Version: uuid.New(), Target: uuid.New(), Deployment: uuid.New()}
		credential, err := randomCredential()
		if err != nil {
			return m, err
		}
		full, _ := json.Marshal(map[string]string{"username": "w12-synthetic", "password": credential})
		sealed, public, err := catalog.SealAuthConfig(full, encryptor)
		if err != nil {
			return m, err
		}
		ids.CredentialDigest = sum(full)
		trust, err := charlie.GenerateLocalTrust(encryptor, charlie.LocalTrustConfig{InstallationID: ids.Connection.String(), BridgeServerDNS: "bridge.w12.svc", MCPServerDNS: "mcp.w12.svc"})
		if err != nil {
			return m, err
		}
		plaintext, err := encryptor.Decrypt(trust.EncryptedLocalTrust)
		if err != nil {
			return m, err
		}
		ids.TrustDigest = sum([]byte(plaintext))
		ids.PublicTrust = trust.Public
		name := fmt.Sprintf("w12-%s-%d", m.RunID.String()[:8], i)
		if err := seedIdentity(ctx, tx, ids, name, sealed, public, k.AgentTokens[i]); err != nil {
			return m, err
		}
		deliveryCredential := sealed
		if i == 0 {
			deliveryCredential = strings.SplitN(sealed, ":", 5)[4]
		}
		if err := seedDelivery(ctx, tx, ids, name, deliveryCredential); err != nil {
			return m, err
		}
		if err := seedCharlie(ctx, tx, ids, name, trust.EncryptedLocalTrust); err != nil {
			return m, err
		}
		intent, err := audit.RecordOutbox(ctx, sqlc.New(tx), audit.Event{Source: "service", Action: "qualification.recovery.seed", ResourceType: "project", ResourceID: ids.Project.String(), ResourceName: name, ActionClass: "system", StatusCode: 200,
			Detail: map[string]any{"project_id": ids.Project.String(), "generation": 7, "credential_digest": ids.CredentialDigest, "previous_digest": strings.Join(m.AuditDigests, ",")}}, "w12-recovery-"+ids.Project.String(), audit.OutboxOptions{CreatedAt: time.Now().UTC().Add(time.Duration(i) * time.Millisecond)})
		if err != nil {
			return m, err
		}
		m.AuditDigests = append(m.AuditDigests, sum(intent.Detail))
		m.Tenants = append(m.Tenants, ids)
	}
	data, _ := json.Marshal(m)
	if _, err := tx.Exec(ctx, `CREATE SCHEMA qualification_w12_recovery;
CREATE TABLE qualification_w12_recovery.marker (owner text NOT NULL,run_id uuid PRIMARY KEY,manifest jsonb NOT NULL);
COMMENT ON TABLE qualification_w12_recovery.marker IS 'Plan032 W12 owned fixture; no production table';`); err != nil {
		return m, err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO qualification_w12_recovery.marker VALUES($1,$2,$3)", m.Owner, m.RunID, data); err != nil {
		return m, err
	}
	return m, tx.Commit(ctx)
}

func seedIdentity(ctx context.Context, tx pgx.Tx, t tenant, name, sealed string, public []byte, agentToken string) error {
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users(id,email,username) VALUES($1,$2,$3)`, []any{t.User, name + "@example.invalid", name}},
		{`INSERT INTO clusters(id,name,display_name,created_by_id) VALUES($1,$2,$2,$3)`, []any{t.Cluster, name, t.User}},
		{`INSERT INTO projects(id,name,display_name,cluster_id,created_by_id) VALUES($1,$2,$2,$3,$4)`, []any{t.Project, name, t.Cluster, t.User}},
		{`INSERT INTO cluster_agent_tokens(cluster_id,token,token_hash) VALUES($1,'',$2)`, []any{t.Cluster, auth.HashOpaqueToken(agentToken)}},
		{`INSERT INTO helm_repositories(id,name,url,owner_project_id,auth_type,auth_config,auth_config_encrypted) VALUES($1,$2,'https://example.invalid/charts',$3,'basic',$4,$5)`, []any{t.Repository, name, t.Project, public, sealed}},
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			return err
		}
	}
	return nil
}

func seedDelivery(ctx context.Context, tx pgx.Tx, t tenant, name, credential string) error {
	renderer, _ := json.Marshal(model.RendererSpec{Kind: model.RendererKustomize, Kustomize: &model.KustomizeSpec{Path: ".", TargetNamespace: "w12"}})
	placement, _ := json.Marshal(model.Placement{ClusterIDs: []uuid.UUID{t.Cluster}, AllClusters: false})
	digest := "sha256:" + strings.Repeat("a", 64)
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO delivery_sources(id,project_id,name,source_type,url,auth_mode,credential_encrypted,credential_key_version) VALUES($1,$2,$3,'helm_http','https://example.invalid/charts','basic',$4,1)`, []any{t.Source, t.Project, name, credential}},
		{`INSERT INTO component_bundles(id,project_id,name) VALUES($1,$2,$3)`, []any{t.Bundle, t.Project, name}},
		{`INSERT INTO component_bundle_versions(id,bundle_id,source_id,version,renderer,requested_revision,spec_digest,renderer_spec) VALUES($1,$2,$3,'fixture-1','kustomize','fixture-revision',$4,$5)`, []any{t.Version, t.Bundle, t.Source, digest, renderer}},
		{`INSERT INTO delivery_targets(id,project_id,name,bundle_version_id,placement,generation) VALUES($1,$2,$3,$4,$5,7)`, []any{t.Target, t.Project, name, t.Version, placement}},
		{`INSERT INTO cluster_deployments(id,target_id,cluster_id,desired_bundle_version_id,desired_generation,observed_generation,desired_spec_digest,phase) VALUES($1,$2,$3,$4,7,6,$5,'applying')`, []any{t.Deployment, t.Target, t.Cluster, t.Version, digest}},
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			return err
		}
	}
	return nil
}

func seedCharlie(ctx context.Context, tx pgx.Tx, t tenant, name, trust string) error {
	digest := "sha256:" + strings.Repeat("b", 64)
	_, err := tx.Exec(ctx, `INSERT INTO charlie_connections(id,installation_id,product_id,product_slug,deployment_id,route_id,central_url,
central_ca_fingerprint,signing_key_id,signing_key_fingerprint,onboarding_schema_version,central_api_version,agent_protocol_version,
chart_version,chart_digest,image_digest,logical_agent_id,bridge_service_name,mcp_service_name,local_trust_material_encrypted,
agent_secret_name,onboarding_package_id,onboarding_package_digest,onboarding_package_expires_at,enrollment_credentials_expires_at,
artifact_credential_expires_at,certificate_expires_at,chart_reference,image_reference)
VALUES($1,$1,$2,'astronomer',$2,$2,'https://example.invalid',$3::text,'owned-disposable-key',$3::text,'fixture-v1','fixture-v1','fixture-v1',
'fixture-1',$3::text,$3::text,$2,'bridge.w12.svc','mcp.w12.svc',$4,$2,$2,$3::text,now()+interval '1 day',now()+interval '1 day',
now()+interval '1 day',now()+interval '1 day','oci://example.invalid/qualification/charlie','example.invalid/qualification/charlie@' || $3::text)`, t.Connection, name, digest, trust)
	return err
}
