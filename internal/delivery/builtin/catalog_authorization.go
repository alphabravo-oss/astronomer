package builtin

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// AuthorizeCatalogSource combines exact builtin identity with independently
// injected destination authority. Both production CA and keyed QA composition
// use this same boundary; a signed location cannot grant network permission.
func AuthorizeCatalogSource(ctx context.Context, loader CatalogLoader, policy httpclient.DestinationPolicy, cluster, project, source uuid.UUID, assignment protocol.DeliveryAssignmentV2) (bool, error) {
	catalog, err := loader.Load(ctx)
	if err != nil {
		return false, errors.New("verified builtin catalog unavailable")
	}
	if !AuthorizesCatalogAssignment(catalog, cluster, project, source, assignment) {
		return false, nil
	}
	endpoint := "https://" + strings.TrimPrefix(assignment.Source.URL, "oci://")
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := policy.ValidateURL(checkCtx, endpoint); err != nil {
		return false, errors.New("builtin registry destination is not approved")
	}
	return true, nil
}
