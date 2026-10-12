package compliance

import (
	"cmp"
	"fmt"
	"math"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// prepareQuotaPlans validates every value before writeSpec can change settings
// or quotas. Signed representable values retain the existing quota semantics.
func prepareQuotaPlans(plans []QuotaPlanSpec) ([]sqlc.UpsertQuotaPlanParams, error) {
	prepared := make([]sqlc.UpsertQuotaPlanParams, len(plans))
	for index, plan := range plans {
		if plan.Name == "default" {
			continue // The writer deliberately never applies this operator-owned plan.
		}
		values := [...]int{plan.MaxClustersPerProject, plan.MaxNamespacesPerProject,
			plan.MaxMembersPerProject, plan.MaxProjectsPerUser, plan.MaxTokensPerUser,
			plan.MaxStreamsPerUser, plan.MaxTotalClusters, plan.MaxTotalUsers}
		fields := [...]string{"max_clusters_per_project", "max_namespaces_per_project",
			"max_members_per_project", "max_projects_per_user", "max_tokens_per_user",
			"max_streams_per_user", "max_total_clusters", "max_total_users"}
		var limits [8]int32
		for field, value := range values {
			limit, err := quotaLimitInt32(value)
			if err != nil {
				return nil, fmt.Errorf("baseline quota field %s exceeds its supported integer range", fields[field])
			}
			limits[field] = limit
		}
		prepared[index] = sqlc.UpsertQuotaPlanParams{
			Name: plan.Name, Enforcement: cmp.Or(plan.Enforcement, "hard"), Description: plan.Description,
			MaxClustersPerProject: limits[0], MaxNamespacesPerProject: limits[1],
			MaxMembersPerProject: limits[2], MaxProjectsPerUser: limits[3],
			MaxTokensPerUser: limits[4], MaxStreamsPerUser: limits[5],
			MaxTotalClusters: limits[6], MaxTotalUsers: limits[7],
		}
	}
	return prepared, nil
}

func quotaLimitInt32(value int) (int32, error) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, fmt.Errorf("quota integer is not representable")
	}
	return int32(value), nil
}
