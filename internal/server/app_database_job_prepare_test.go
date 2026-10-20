package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/config"
)

func TestDatabaseJobPreparationRefusesUnownedInputsBeforeTrustRead(t *testing.T) {
	command := DatabaseJobPreparation{FeedDigest: "sha256:" + strings.Repeat("a", 64), ClaimID: uuid.MustParse("857e0a17-a815-49b8-b62c-52e404b00667"), StagingRoot: "/owned/staging", CacheRoot: "/owned/cache", CoordinationRoot: "/owned/coordination", WorkloadRoot: "/owned/workload", WorkloadRepository: "registry.example/workload/app", WorkloadDigest: "sha256:" + strings.Repeat("b", 64), Architecture: "amd64"}
	// Deliberately absent trust/settings: invalid command DATA must fail before
	// constructing any loader, snapshot or registry client.
	for _, name := range []string{"nil-config", "disabled", "missing-feed", "mutable-feed", "nil-claim", "non-v4-claim", "relative-path", "unclean-path", "shared-path", "missing-workload-root", "relative-workload-root", "unclean-workload-root", "overlapping-workload-root", "missing-workload-repository", "missing-workload-digest", "mutable-workload-digest", "unsupported-workload-architecture"} {
		t.Run(name, func(t *testing.T) {
			cfg := &config.Config{DeliveryDatabaseUpdatesEnabled: true}
			request := command
			expected := "database Job preparation optional feature or identity invalid"
			switch name {
			case "nil-config":
				cfg = nil
			case "disabled":
				cfg.DeliveryDatabaseUpdatesEnabled = false
			case "missing-feed":
				request.FeedDigest = ""
			case "mutable-feed":
				request.FeedDigest = "latest"
			case "nil-claim":
				request.ClaimID = uuid.Nil
			case "non-v4-claim":
				request.ClaimID = uuid.NewSHA1(uuid.Nil, []byte("not-a-Job-claim"))
			case "relative-path":
				request.StagingRoot = "relative"
				expected = "database Job preparation owned path invalid"
			case "unclean-path":
				request.CacheRoot = "/owned/../cache"
				expected = "database Job preparation owned path invalid"
			case "shared-path":
				request.CoordinationRoot = request.CacheRoot
				expected = "database Job preparation owned paths overlap"
			case "missing-workload-root":
				request.WorkloadRoot = ""
				expected = "database Job preparation owned path invalid"
			case "relative-workload-root":
				request.WorkloadRoot = "relative"
				expected = "database Job preparation owned path invalid"
			case "unclean-workload-root":
				request.WorkloadRoot = "/owned/../workload"
				expected = "database Job preparation owned path invalid"
			case "overlapping-workload-root":
				request.WorkloadRoot = request.CacheRoot
				expected = "database Job preparation owned paths overlap"
			case "missing-workload-repository":
				request.WorkloadRepository = ""
				expected = "owned Job workload identity invalid"
			case "missing-workload-digest":
				request.WorkloadDigest = ""
				expected = "owned Job workload identity invalid"
			case "mutable-workload-digest":
				request.WorkloadDigest = "latest"
				expected = "owned Job workload identity invalid"
			case "unsupported-workload-architecture":
				request.Architecture = "386"
				expected = "owned Job workload identity invalid"
			}
			err := PrepareDatabaseCacheJob(t.Context(), cfg, request)
			if err == nil || err.Error() != expected {
				t.Fatalf("invalid input reached trust/bootstrap: %v", err)
			}
		})
	}
}
func TestDatabaseJobPreparationCancellationCannotConstructAuthority(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	command := DatabaseJobPreparation{FeedDigest: "sha256:" + strings.Repeat("a", 64), ClaimID: uuid.MustParse("857e0a17-a815-49b8-b62c-52e404b00667"), StagingRoot: "/owned/staging", CacheRoot: "/owned/cache", CoordinationRoot: "/owned/coordination", WorkloadRoot: "/owned/workload", WorkloadRepository: "registry.example/workload/app", WorkloadDigest: "sha256:" + strings.Repeat("b", 64), Architecture: "amd64"}
	if err := PrepareDatabaseCacheJob(ctx, &config.Config{DeliveryDatabaseUpdatesEnabled: true}, command); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not retained before trust capture: %v", err)
	}
}
