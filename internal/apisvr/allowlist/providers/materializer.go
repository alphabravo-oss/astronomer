package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type credentialQueries interface {
	GetCloudCredentialByID(context.Context, uuid.UUID) (sqlc.CloudCredential, error)
	ListCloudCredentialsForCluster(context.Context, uuid.UUID) ([]sqlc.CloudCredential, error)
}

type credentialDecryptor interface {
	DecryptBytes(string) ([]byte, error)
}

// SQLCredentialMaterializer resolves only credentials explicitly linked to a
// cluster, verifies provider type, decrypts them for one call, and clears the
// plaintext byte buffer immediately after JSON decoding.
type SQLCredentialMaterializer struct {
	queries   credentialQueries
	decryptor credentialDecryptor
}

// ClusterFromSQLC is the single mapping contract shared by the API capability
// response and the worker reconciler. Provider-specific metadata lives in
// annotations because Astronomer registers, but does not provision, clusters.
func ClusterFromSQLC(row sqlc.Cluster) Cluster {
	annotations := map[string]string{}
	_ = json.Unmarshal(row.Annotations, &annotations)
	credentialID, _ := uuid.Parse(firstAnnotation(annotations,
		"astronomer.io/cloud-credential-id",
		"cloud.astronomer.io/credential-id",
	))
	return Cluster{
		ID:                 row.ID,
		Provider:           row.Provider,
		Name:               firstNonEmpty(firstAnnotation(annotations, "astronomer.io/provider-cluster-name"), row.Name),
		Region:             row.Region,
		ResourceGroup:      firstAnnotation(annotations, "astronomer.io/azure-resource-group", "azure.com/resource-group"),
		ProjectID:          firstAnnotation(annotations, "astronomer.io/gcp-project-id", "google.com/project-id"),
		CredentialID:       credentialID,
		ProviderResourceID: firstAnnotation(annotations, "astronomer.io/provider-cluster-id", "digitalocean.com/cluster-id"),
		Annotations:        annotations,
	}
}

func firstAnnotation(annotations map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(annotations[key]); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func NewSQLCredentialMaterializer(queries credentialQueries, decryptor credentialDecryptor) (*SQLCredentialMaterializer, error) {
	if queries == nil {
		return nil, fmt.Errorf("cloud credential queries are required")
	}
	if decryptor == nil {
		return nil, fmt.Errorf("cloud credential decryptor is required")
	}
	return &SQLCredentialMaterializer{queries: queries, decryptor: decryptor}, nil
}

func (m *SQLCredentialMaterializer) ResolveForCluster(ctx context.Context, cluster Cluster, provider ProviderID) (map[string]string, error) {
	var candidates []sqlc.CloudCredential
	if cluster.CredentialID != uuid.Nil {
		row, err := m.queries.GetCloudCredentialByID(ctx, cluster.CredentialID)
		if err != nil {
			return nil, fmt.Errorf("load explicitly selected cloud credential %s: %w", cluster.CredentialID, err)
		}
		if !credentialTargetsCluster(row.TargetRefs, cluster.ID) {
			return nil, fmt.Errorf("cloud credential %s is not targeted to cluster %s", row.ID, cluster.ID)
		}
		candidates = []sqlc.CloudCredential{row}
	} else {
		rows, err := m.queries.ListCloudCredentialsForCluster(ctx, cluster.ID)
		if err != nil {
			return nil, fmt.Errorf("list cloud credentials for cluster: %w", err)
		}
		for _, row := range rows {
			if credentialProviderMatches(row.Provider, provider) {
				candidates = append(candidates, row)
			}
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no %s cloud credential targets cluster %s", provider, cluster.ID)
	}
	if len(candidates) > 1 {
		return nil, fmt.Errorf("multiple %s cloud credentials target cluster %s; set annotation astronomer.io/cloud-credential-id", provider, cluster.ID)
	}
	row := candidates[0]
	if !credentialProviderMatches(row.Provider, provider) {
		return nil, fmt.Errorf("cloud credential %s has provider %q, incompatible with %s", row.ID, row.Provider, provider)
	}
	plain, err := m.decryptor.DecryptBytes(row.DataEncrypted)
	if err != nil {
		return nil, fmt.Errorf("decrypt cloud credential %s: %w", row.ID, err)
	}
	defer clear(plain)
	values := map[string]string{}
	if err := json.Unmarshal(plain, &values); err != nil {
		return nil, fmt.Errorf("decode cloud credential %s: %w", row.ID, err)
	}
	return values, nil
}

func credentialProviderMatches(stored string, provider ProviderID) bool {
	stored = strings.ToLower(strings.TrimSpace(stored))
	switch provider {
	case ProviderEKS:
		return stored == "aws"
	case ProviderGKE:
		return stored == "gcp"
	case ProviderAKS:
		return stored == "azure"
	case ProviderDOKS:
		return stored == "digitalocean" || stored == "doks"
	default:
		return false
	}
}

func credentialTargetsCluster(raw json.RawMessage, clusterID uuid.UUID) bool {
	var refs []struct {
		ClusterID uuid.UUID `json:"cluster_id"`
	}
	if err := json.Unmarshal(raw, &refs); err != nil {
		return false
	}
	for _, ref := range refs {
		if ref.ClusterID == clusterID {
			return true
		}
	}
	return false
}
