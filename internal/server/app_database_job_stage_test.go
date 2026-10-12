package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/google/uuid"
)

func TestDatabaseSeparateStageKeepsCanonicalPreTrustRefusals(t *testing.T) {
	command := DatabaseJobPreparation{FeedDigest: "sha256:" + strings.Repeat("a", 64), ClaimID: uuid.MustParse("857e0a17-a815-49b8-b62c-52e404b00667"), StagingRoot: "/owned/staging", CacheRoot: "/owned/cache", CoordinationRoot: "/owned/coordination", WorkloadRoot: "/owned/workload", WorkloadRepository: "registry.example/workload/app", WorkloadDigest: "sha256:" + strings.Repeat("b", 64), Architecture: "amd64"}
	for _, name := range []string{"disabled", "mutable-feed", "overlapping-workload", "canceled"} {
		t.Run(name, func(t *testing.T) {
			cfg := &config.Config{DeliveryDatabaseUpdatesEnabled: true}
			request := command
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			expected := "database Job preparation optional feature or identity invalid"
			switch name {
			case "disabled":
				cfg.DeliveryDatabaseUpdatesEnabled = false
			case "mutable-feed":
				request.FeedDigest = "latest"
			case "overlapping-workload":
				request.WorkloadRoot = request.CacheRoot
				expected = "database Job preparation owned paths overlap"
			case "canceled":
				cancel()
			}
			err := StageDatabaseScanInput(ctx, cfg, request)
			if name == "canceled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("staging lost cancellation: %v", err)
				}
				return
			}
			if err == nil || err.Error() != expected {
				t.Fatalf("staging bypassed canonical pre-trust validation: %v", err)
			}
		})
	}
}
