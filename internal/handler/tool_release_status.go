package handler

import (
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func installedToolPlanStatus(tool sqlc.ClusterTool, rows []sqlc.InstalledChart, latest sqlc.ToolOperation) (string, any) {
	var plan []toolRelease
	var env toolOperationEnvelope
	if len(latest.Payload) > 0 && json.Unmarshal(latest.Payload, &env) == nil {
		plan = env.Releases
	}
	if len(plan) == 0 {
		var err error
		plan, err = buildToolReleasePlan(tool, "", "")
		if err != nil {
			return "unknown", "Tool release plan is invalid"
		}
	}
	installed := map[string]string{}
	for _, row := range rows {
		installed[row.Namespace+"/"+row.ReleaseName] = normalizeToolStatus(row.Status)
	}
	ready := 0
	for _, release := range plan {
		if installed[release.Namespace+"/"+release.ReleaseName] == "installed" {
			ready++
		}
	}
	if ready != len(plan) {
		return "failed", fmt.Sprintf("%d of %d tool releases are installed; inspect or retry the operation", ready, len(plan))
	}
	return "installed", nil
}
