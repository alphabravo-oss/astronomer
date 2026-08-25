package server

import (
	"context"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// nodeOperationReadAuthorizer checks current capability after the handler has
// loaded the durable receipt and bound it to the route's cluster and node.
// Readers may inspect any bound receipt; mutation-only callers retain access
// while they still hold the verb required by the receipt's original action.
func nodeOperationReadAuthorizer(engine *rbac.Engine, querier appmiddleware.RBACQuerier) func(context.Context, string, string) (bool, error) {
	return func(ctx context.Context, clusterID, action string) (bool, error) {
		user, ok := appmiddleware.GetAuthenticatedUser(ctx)
		if !ok || user == nil {
			return false, nil
		}
		bindings, err := querier.GetUserBindings(ctx, user.ID)
		if err != nil {
			return false, err
		}
		clusterUUID, err := uuid.Parse(clusterID)
		if err != nil {
			return false, nil
		}
		primary := rbac.VerbUpdate
		if action == "drain" {
			primary = rbac.VerbManage
		}
		return engine.CheckPermission(bindings, rbac.ResourceNodes, rbac.VerbRead, clusterUUID, uuid.Nil) ||
			engine.CheckPermission(bindings, rbac.ResourceNodes, primary, clusterUUID, uuid.Nil), nil
	}
}
