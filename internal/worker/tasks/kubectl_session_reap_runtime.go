package tasks

import "github.com/hibiken/asynq"

func (runtime KubectlSessionReapRuntime) Validate() error {
	runtime = runtime.normalized()
	return validateRequiredRuntimeDependencies("kubectl session reaper", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "requester", value: runtime.Deps.Requester},
		{name: "leader", value: runtime.Leader},
		{name: "logger", value: runtime.Log},
	})
}

// HandlerBindings binds this optional task even while disabled. Feature-aware
// startup validation requires its dependencies only when the feature is on.
func (runtime KubectlSessionReapRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	return map[string]asynq.HandlerFunc{KubectlSessionReapType: runtime.normalized().HandleKubectlSessionReap}, nil
}
