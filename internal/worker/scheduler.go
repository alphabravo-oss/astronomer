package worker

import (
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"
)

// Scheduler manages all periodic task producers from the same typed registry
// used to construct worker muxes. This prevents a scheduled task from drifting
// onto a queue whose owning process cannot execute it.
type Scheduler struct {
	scheduler *asynq.Scheduler
	log       *slog.Logger
	features  SchedulerFeatures
}

type SchedulerFeatures struct {
	CRDOwnership bool
}

func NewScheduler(redisURL string, log *slog.Logger, configured ...SchedulerFeatures) (*Scheduler, error) {
	redisOpt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL %q: %w", redisURL, err)
	}
	features := SchedulerFeatures{}
	if len(configured) > 0 {
		features = configured[0]
	}
	return &Scheduler{scheduler: asynq.NewScheduler(redisOpt, nil), log: log, features: features}, nil
}

func (s *Scheduler) RegisterPeriodicTasks() error {
	if err := ValidateTaskRegistry(); err != nil {
		return fmt.Errorf("validate task registry before scheduling: %w", err)
	}
	for _, spec := range scheduledTaskSpecsForFeatures(s.features) {
		task := asynq.NewTask(spec.TaskType, nil)
		options := []asynq.Option{}
		if spec.Queue != "default" {
			options = append(options, asynq.Queue(spec.Queue))
		}
		entryID, err := s.scheduler.Register(spec.Cron, task, options...)
		if err != nil {
			s.log.Error("failed to register periodic task", "task", spec.TaskType, "owner", spec.Owner, "queue", spec.Queue, "error", err)
			return err
		}
		s.log.Info("registered periodic task", "task", spec.Description, "task_type", spec.TaskType, "owner", spec.Owner, "queue", spec.Queue, "schedule", spec.Cron, "entry_id", entryID)
	}
	return nil
}

func (s *Scheduler) Start() error {
	s.log.Info("starting scheduler")
	return s.scheduler.Start()
}

func (s *Scheduler) Shutdown() {
	s.log.Info("shutting down scheduler")
	s.scheduler.Shutdown()
}
