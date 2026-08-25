package tasks

import (
	"fmt"

	"github.com/hibiken/asynq"
)

// ClusterTemplateRuntime keeps template execution and its recovery sweep on
// one immutable dependency graph.
type ClusterTemplateRuntime struct {
	Deps             ClusterTemplateApplyDeps
	RecoveryEnqueuer FailedApplyEnqueuer
}

func (runtime ClusterTemplateRuntime) Validate() error {
	return validateRequiredRuntimeDependencies("cluster template", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "installer", value: runtime.Deps.Installer},
		{name: "recovery_enqueuer", value: runtime.RecoveryEnqueuer},
	})
}

func (runtime ClusterTemplateRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if err := runtime.Validate(); err != nil {
		return nil, fmt.Errorf("validate cluster template runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{
		ClusterTemplateApplyType:      runtime.HandleClusterTemplateApply,
		ClusterTemplateDriftCheckType: runtime.HandleClusterTemplateDriftCheck,
	}, nil
}
