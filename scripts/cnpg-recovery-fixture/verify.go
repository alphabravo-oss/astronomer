package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel/connectauth"
)

func verify(ctx context.Context, pool *pgxpool.Pool, keyPath, negative string, expected manifest) error {
	k, encryptor, err := recoveredKeys(keyPath, expected.KeyRefDigest, negative)
	if err != nil {
		return err
	}
	var stored []byte
	if err := pool.QueryRow(ctx, "SELECT manifest FROM qualification_w12_recovery.marker WHERE owner=$1 AND run_id=$2", expected.Owner, expected.RunID).Scan(&stored); err != nil {
		return errors.New("owned source marker missing")
	}
	var restored manifest
	if decodeClosed(stored, &restored) != nil {
		return errors.New("invalid restored source marker")
	}
	original, _ := json.Marshal(expected)
	actual, _ := json.Marshal(restored)
	if string(original) != string(actual) {
		return errors.New("source marker differs after restore")
	}
	q := sqlc.New(pool)
	for i, t := range expected.Tenants {
		repo, err := q.GetHelmRepositoryByID(ctx, t.Repository)
		if err != nil {
			return errors.New("restored repository missing")
		}
		if !repo.OwnerProjectID.Valid || uuid.UUID(repo.OwnerProjectID.Bytes) != t.Project {
			return errors.New("repository tenant identity changed")
		}
		full, err := catalog.ResolveAuthConfig(repo, encryptor)
		if err != nil || sum(full) != t.CredentialDigest {
			return errors.New("production catalog credential recovery failed")
		}
		connection, err := q.GetCharlieConnection(ctx, t.Connection)
		if err != nil || connection.InstallationID != t.Connection {
			return errors.New("restored Charlie connection missing")
		}
		plaintext, err := encryptor.Decrypt(connection.LocalTrustMaterialEncrypted)
		if err != nil || sum([]byte(plaintext)) != t.TrustDigest {
			return errors.New("Charlie old/current encrypted trust recovery failed")
		}
		if err := verifyTLS(t.PublicTrust, []byte(plaintext)); err != nil {
			return errors.New("restored Charlie TLS private key unusable")
		}
		agent, err := connectauth.Validate(ctx, q, t.Cluster, k.AgentTokens[i])
		if err != nil || agent.Kind != connectauth.KindAgent || agent.AgentToken.ClusterID != t.Cluster {
			return errors.New("production restored agent credential rejected")
		}
		if _, err := connectauth.Validate(ctx, q, expected.Tenants[1-i].Cluster, k.AgentTokens[i]); err == nil {
			return errors.New("agent credential crossed tenant cluster boundary")
		}
		if _, err := connectauth.Validate(ctx, q, t.Cluster, "not-a-restored-token"); err == nil {
			return errors.New("unrecognized agent token accepted")
		}
		var credential string
		if err := pool.QueryRow(ctx, "SELECT credential_encrypted FROM delivery_sources WHERE id=$1 AND project_id=$2", t.Source, t.Project).Scan(&credential); err != nil {
			return errors.New("delivery source tenant mismatch")
		}
		decrypted, err := encryptor.Decrypt(credential)
		if err != nil || sum([]byte(decrypted)) != t.CredentialDigest {
			return errors.New("delivery source credential unavailable")
		}
		if err := verifyDomain(ctx, pool, t); err != nil {
			return err
		}
	}
	rows, err := pool.Query(ctx, "SELECT detail FROM audit_outbox WHERE action='qualification.recovery.seed' ORDER BY event_created_at,id")
	if err != nil {
		return err
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		var detail []byte
		if err := rows.Scan(&detail); err != nil {
			return err
		}
		if index >= len(expected.AuditDigests) || sum(detail) != expected.AuditDigests[index] {
			return errors.New("audit intent identity/order/detail differs")
		}
		index++
	}
	if rows.Err() != nil || index != len(expected.AuditDigests) {
		return errors.New("audit intent count differs")
	}
	return nil
}

func verifyDomain(ctx context.Context, pool *pgxpool.Pool, t tenant) error {
	var cluster, project uuid.UUID
	var desired, observed, generation int64
	err := pool.QueryRow(ctx, `SELECT p.cluster_id,dt.project_id,d.desired_generation,d.observed_generation,dt.generation
FROM projects p JOIN delivery_targets dt ON dt.project_id=p.id JOIN cluster_deployments d ON d.target_id=dt.id
WHERE p.id=$1 AND dt.id=$2 AND d.id=$3 AND d.cluster_id=$4`, t.Project, t.Target, t.Deployment, t.Cluster).Scan(&cluster, &project, &desired, &observed, &generation)
	if err != nil || cluster != t.Cluster || project != t.Project || desired != 7 || observed != 6 || generation != 7 {
		return errors.New("restored tenant/deployment generation mismatch")
	}
	return nil
}

func verifyTLS(public publicTrust, plaintext []byte) error {
	var material struct {
		Bridge string `json:"bridge_client_private_key_pem"`
		MCP    string `json:"mcp_server_private_key_pem"`
	}
	if json.Unmarshal(plaintext, &material) != nil {
		return errors.New("invalid production Charlie trust serialization")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(public.CACertificatePEM)) {
		return errors.New("Charlie CA unavailable")
	}
	for _, check := range []struct {
		cert, key string
		usage     x509.ExtKeyUsage
		dns       string
		identity  string
	}{
		{public.BridgeClientCertificate, material.Bridge, x509.ExtKeyUsageClientAuth, "", public.BridgeClientIdentityURI},
		{public.MCPServerCertificate, material.MCP, x509.ExtKeyUsageServerAuth, "mcp.w12.svc", public.MCPServerIdentityURI},
	} {
		if _, err := tls.X509KeyPair([]byte(check.cert), []byte(check.key)); err != nil {
			return errors.New("Charlie key/certificate pair differs")
		}
		block, _ := pem.Decode([]byte(check.cert))
		if block == nil {
			return errors.New("Charlie certificate malformed")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return err
		}
		if len(certificate.URIs) != 1 || certificate.URIs[0].String() != check.identity {
			return errors.New("restored Charlie identity differs")
		}
		if _, err := certificate.Verify(x509.VerifyOptions{Roots: roots, DNSName: check.dns, KeyUsages: []x509.ExtKeyUsage{check.usage}}); err != nil {
			return fmt.Errorf("Charlie certificate usage invalid: %w", err)
		}
	}
	return nil
}
