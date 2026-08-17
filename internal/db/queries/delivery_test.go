package queries_test

import (
	"os"
	"strings"
	"testing"
)

// Public delivery-source reads must never select the encrypted credential or
// private CA. Resolution workers and the authenticated cluster-bound assignment
// provider are the only internal secret projections.
func TestDeliverySourcePublicQueriesExcludeSecretColumns(t *testing.T) {
	raw, err := os.ReadFile("delivery.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, marker := range []string{
		"-- name: CreateDeliverySource :one",
		"-- name: GetDeliverySource :one",
		"-- name: ListDeliverySources :many",
		"-- name: UpdateDeliverySourceStatus :one",
		"-- name: UpdateDeliverySource :one",
		"-- name: RotateDeliverySourceCredential :one",
		"-- name: RevokeDeliverySource :one",
	} {
		query := namedQuery(t, text, marker)
		for _, prohibited := range []string{"credential_encrypted", "ca_bundle_encrypted"} {
			// The create/rotate statements necessarily write ciphertext. Only
			// their RETURNING projection is part of the public response boundary.
			projection := query
			if before, after, ok := strings.Cut(query, "RETURNING"); ok {
				_ = before
				projection = after
			}
			if strings.Contains(projection, prohibited) {
				t.Fatalf("%s returns secret column %s", marker, prohibited)
			}
		}
	}
}

func TestDeliverySecretProjectionsAreClosedAndInternal(t *testing.T) {
	raw, err := os.ReadFile("delivery.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, secret := range []string{"s.credential_encrypted", "s.ca_bundle_encrypted"} {
		if got := strings.Count(text, secret); got != 2 {
			t.Fatalf("%s appears %d times; want resolution-worker and assignment-provider projections", secret, got)
		}
	}
	for _, marker := range []string{"-- name: GetDeliverySourceResolutionWork :one", "-- name: ListClusterDeliveryAssignments :many"} {
		query := namedQuery(t, text, marker)
		if !strings.Contains(query, "s.credential_encrypted") || !strings.Contains(query, "s.ca_bundle_encrypted") {
			t.Fatalf("%s must project both encrypted source fields", marker)
		}
	}
}

func TestDeliveryWorkersUseSkipLockedAndFencedCAS(t *testing.T) {
	raw, err := os.ReadFile("delivery.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{
		"FOR UPDATE SKIP LOCKED",
		"fencing_generation = sqlc.arg(expected_rollout_fence)",
		"lease_owner = sqlc.arg(expected_lease_owner)",
		"fence = sqlc.arg(expected_fence)",
		"fencing_generation = sqlc.arg(expected_fence)",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("delivery queries are missing HA safety clause %q", required)
		}
	}
}

func namedQuery(t *testing.T, text, marker string) string {
	t.Helper()
	start := strings.Index(text, marker)
	if start < 0 {
		t.Fatalf("missing query marker %s", marker)
	}
	rest := text[start:]
	if next := strings.Index(rest[len(marker):], "-- name:"); next >= 0 {
		return rest[:len(marker)+next]
	}
	return rest
}
