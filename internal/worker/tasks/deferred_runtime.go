package tasks

import (
	"fmt"

	"github.com/hibiken/asynq"
)

var requiredDeferredReplayTypes = []string{
	"cluster.delete", "project.delete", "tool.install", "tool.upgrade",
	"tool.uninstall", "helm.install", "helm.uninstall", "cluster_template.apply",
}

type DeferredRuntime struct{ Deps DeferredDispatchDeps }

func (runtime DeferredRuntime) normalized() DeferredRuntime {
	if runtime.Deps.Replayers != nil {
		cloned := make(map[string]DeferredReplayer, len(runtime.Deps.Replayers))
		for operationType, replay := range runtime.Deps.Replayers {
			cloned[operationType] = replay
		}
		runtime.Deps.Replayers = cloned
	}
	return runtime
}

func (runtime DeferredRuntime) Validate() error {
	runtime = runtime.normalized()
	required := []requiredRuntimeDependency{{name: "queries", value: runtime.Deps.Queries}}
	for _, operationType := range requiredDeferredReplayTypes {
		required = append(required, requiredRuntimeDependency{
			name: "replayer[" + operationType + "]", value: runtime.Deps.Replayers[operationType],
		})
	}
	return validateRequiredRuntimeDependencies("deferred dispatch", required)
}

func (runtime DeferredRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	runtime = runtime.normalized()
	if err := runtime.Validate(); err != nil {
		return nil, fmt.Errorf("validate deferred runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{DispatchDeferredType: runtime.HandleDispatchDeferred}, nil
}
