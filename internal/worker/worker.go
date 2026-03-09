package worker

import (
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/hibiken/asynq"
)

// Task type constants
const (
	TypeHealthCheck        = "cluster:health_check"
	TypeAlertEvaluation    = "alert:evaluate"
	TypeCatalogSync        = "catalog:sync"
	TypeMetricsAggregation = "metrics:aggregate"
	TypeBackupExecution    = "backup:execute"
	TypeSecurityScan       = "security:scan"
	TypeNotificationSend   = "notification:send"
	TypeAgentManifest      = "agent:generate_manifest"
)

// Worker wraps the Asynq server for processing background tasks.
type Worker struct {
	server *asynq.Server
	mux    *asynq.ServeMux
	log    *slog.Logger
}

// NewWorker creates a new Asynq-based background worker.
func NewWorker(redisURL string, log *slog.Logger) *Worker {
	redisOpt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		log.Error("failed to parse redis URL, falling back to default", "error", err)
		redisOpt = asynq.RedisClientOpt{Addr: "localhost:6379"}
	}

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: 10,
		Queues: map[string]int{
			"critical": 6,
			"default":  3,
			"low":      1,
		},
	})

	return &Worker{
		server: srv,
		mux:    asynq.NewServeMux(),
		log:    log,
	}
}

// RegisterHandlers sets up all task handlers on the mux.
func (w *Worker) RegisterHandlers() {
	w.mux.HandleFunc(TypeHealthCheck, tasks.HandleHealthCheck)
	w.mux.HandleFunc(TypeAlertEvaluation, tasks.HandleAlertEvaluation)
	w.mux.HandleFunc(TypeCatalogSync, tasks.HandleCatalogSync)
	w.mux.HandleFunc(TypeMetricsAggregation, tasks.HandleMetricsAggregation)
	w.mux.HandleFunc(TypeBackupExecution, tasks.HandleBackupExecution)
	w.mux.HandleFunc(TypeSecurityScan, tasks.HandleSecurityScan)
	w.mux.HandleFunc(TypeNotificationSend, tasks.HandleNotificationSend)
	w.mux.HandleFunc(TypeAgentManifest, tasks.HandleAgentManifest)

	w.log.Info("registered all task handlers")
}

// Start begins processing tasks. This blocks until Shutdown is called.
func (w *Worker) Start() error {
	w.log.Info("starting worker")
	return w.server.Start(w.mux)
}

// Shutdown gracefully stops the worker.
func (w *Worker) Shutdown() {
	w.log.Info("shutting down worker")
	w.server.Shutdown()
}
