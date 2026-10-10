package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/vault"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"
)

type observedVaultQuerier struct{ row sqlc.VaultConnection }

func (q observedVaultQuerier) GetVaultConnectionByName(context.Context, string) (sqlc.VaultConnection, error) {
	return q.row, nil
}
func (q observedVaultQuerier) GetVaultConnectionByID(context.Context, uuid.UUID) (sqlc.VaultConnection, error) {
	return q.row, nil
}
func (q observedVaultQuerier) GetProjectDefaultVaultConnection(context.Context, uuid.UUID) (pgtype.UUID, error) {
	return pgtype.UUID{}, errors.New("unused")
}

type delayedVaultClient struct{ entered, release chan struct{} }

func (c delayedVaultClient) FetchSecret(ctx context.Context, _, _ string) (map[string]any, error) {
	close(c.entered)
	select {
	case <-c.release:
		return map[string]any{"value": "synthetic-public-fixture"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func observedVaultMetricChildren(t *testing.T) int {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, family := range families {
		if family.GetName() != "astronomer_vault_resolves_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "astronomer_instance_id" && label.GetValue() == observability.InstanceID() {
					count++
				}
			}
		}
	}
	return count
}

func TestVaultObserverCapturedResolutionCannotResurrectRetiredOwner(t *testing.T) {
	previous := observability.InstanceID()
	observability.SetInstanceID(t.Name())
	t.Cleanup(func() {
		observability.RetireVaultMetrics(nil, observability.VaultMetricsWatermark())
		observability.SetInstanceID(previous)
	})
	row := sqlc.VaultConnection{ID: uuid.New(), Name: "synthetic-owned", Enabled: true, DefaultMount: "secret"}
	entered, release := make(chan struct{}), make(chan struct{})
	resolver := vault.NewResolverWithFactory(observedVaultQuerier{row}, nil, func(sqlc.VaultConnection, string) (vault.Client, error) {
		return delayedVaultClient{entered, release}, nil
	})
	observer := newVaultMetricsObserver(nil)
	resolver.SetObserver(observer)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		value, err := resolver.Resolve(ctx, uuid.New(), "${vault://synthetic-owned/secret/public#value}")
		if err == nil && value != "synthetic-public-fixture" {
			err = errors.New("resolver changed returned value")
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("resolver did not reach actual network seam")
	}
	observability.RetireVaultMetricOwner(row.ID)
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("resolver did not complete bounded callback")
	}
	if children := observedVaultMetricChildren(t); children != 0 {
		t.Fatalf("actual late resolver callback recreated%d children", children)
	}
}

func TestVaultObserverUnverifiedNamesAggregateMetricsPreserveAudit(t *testing.T) {
	previous := observability.InstanceID()
	observability.SetInstanceID(t.Name())
	t.Cleanup(func() { observability.SetInstanceID(previous) })
	q := &fakeAuditQuerier{}
	observer := newVaultMetricsObserver(q)
	names := []string{"synthetic-missing-one", "synthetic-missing-two", "synthetic-missing-three"}
	for _, name := range names {
		observer.OnFailed(context.Background(), name, vault.Reference{Raw: "public-reference"}, errors.New("404 not found"))
	}
	if children := observedVaultMetricChildren(t); children != 1 {
		t.Fatalf("unverified names created%d children", children)
	}
	if len(q.rows) != len(names) {
		t.Fatalf("auditrows=%d want%d", len(q.rows), len(names))
	}
	for i, row := range q.rows {
		var detail map[string]any
		if err := json.Unmarshal(row.Detail, &detail); err != nil {
			t.Fatal(err)
		}
		if detail["connection"] != names[i] {
			t.Fatal("metric label projection changed audit connection")
		}
	}
}
