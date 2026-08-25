package tasks

import (
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

type SecurityIngestRuntime struct {
	Deps   SecurityIngestDeps
	Leader LeaderElector
}

func (runtime SecurityIngestRuntime) normalized() SecurityIngestRuntime {
	if runtime.Deps.Log == nil {
		runtime.Deps.Log = slog.Default()
	}
	if runtime.Deps.Now == nil {
		runtime.Deps.Now = time.Now
	}
	if strings.TrimSpace(runtime.Deps.Owner) == "" {
		host, _ := os.Hostname()
		runtime.Deps.Owner = strings.Trim(strings.TrimSpace(host)+"-"+uuid.NewString(), "-")
	}
	return runtime
}

func (runtime SecurityIngestRuntime) Validate() error {
	runtime = runtime.normalized()
	return validateRequiredRuntimeDependencies("security ingest", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "k8s_fetcher", value: runtime.Deps.K8s},
		{name: "task_outbox", value: runtime.Deps.Outbox},
		{name: "leader", value: runtime.Leader},
		{name: "logger", value: runtime.Deps.Log},
		{name: "clock", value: runtime.Deps.Now},
		{name: "owner", value: runtime.Deps.Owner},
	})
}

func (runtime SecurityIngestRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	runtime = runtime.normalized()
	if err := runtime.Validate(); err != nil {
		return nil, err
	}
	return map[string]asynq.HandlerFunc{
		SecurityIngestType:         runtime.HandleSecurityIngest,
		SecurityIngestRecoveryType: runtime.HandleSecurityIngestRecovery,
	}, nil
}
