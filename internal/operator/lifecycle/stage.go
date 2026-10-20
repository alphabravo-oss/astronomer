package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Step is what a stage tells the pipeline to do next.
type Step struct {
	stop   bool
	result reconcile.Result
}

// Next continues with the following stage.
func Next() Step { return Step{} }

// Done ends the reconcile successfully with result; later stages do not run.
func Done(result reconcile.Result) Step { return Step{stop: true, result: result} }

// Stage is one named step of a reconcile. S is the per-reconcile state the
// stages share (the fetched object, the resolved spec, the computed status).
type Stage[S any] struct {
	// Name identifies the stage in logs, metrics and the observer; unique
	// within a pipeline.
	Name string
	// FailureReason, when non-empty, is the Ready=False condition reason the
	// pipeline records through its failure hook when Run returns an error.
	// Stages without one (fetch, finalizer writes) fail without touching
	// status.
	FailureReason string
	Run           func(ctx context.Context, state *S) (Step, error)
}

// Outcome classifies how a stage ended.
type Outcome string

const (
	OutcomeContinue Outcome = "continue"
	OutcomeDone     Outcome = "done"
	OutcomeError    Outcome = "error"
	OutcomePanic    Outcome = "panic"
)

// Observer receives every stage transition. It is called synchronously from
// the reconciling goroutine while the object's lock is held.
type Observer interface {
	StageStarted(ctx context.Context, controller, stage, key string)
	StageFinished(ctx context.Context, controller, stage, key string, outcome Outcome, elapsed time.Duration)
}

// PipelineConfig assembles a Pipeline.
type PipelineConfig[S any] struct {
	// Controller names the pipeline in logs and metrics.
	Controller string
	// Stages run in order. Registered once; the pipeline is immutable.
	Stages []Stage[S]
	// NewState builds the empty per-reconcile state for a request.
	NewState func(reconcile.Request) *S
	// OnFailure records a failing stage's FailureReason on the object. Its
	// error is joined to the stage error.
	OnFailure func(ctx context.Context, state *S, reason string, err error) error
	// Locks serializes reconciles per object. Nil gets a private lock set.
	Locks    *KeyedLock
	Log      *slog.Logger
	Observer Observer
}

// Pipeline is a reconcile.Reconciler that runs a fixed chain of stages under
// a per-object lock.
type Pipeline[S any] struct {
	cfg PipelineConfig[S]
}

var _ reconcile.Reconciler = (*Pipeline[struct{}])(nil)

// NewPipeline validates cfg: a controller name, a state constructor, and at
// least one stage, every stage named uniquely and runnable.
func NewPipeline[S any](cfg PipelineConfig[S]) (*Pipeline[S], error) {
	if cfg.Controller == "" {
		return nil, errors.New("lifecycle: pipeline controller name is required")
	}
	if cfg.NewState == nil {
		return nil, fmt.Errorf("lifecycle: pipeline %s: NewState is required", cfg.Controller)
	}
	if len(cfg.Stages) == 0 {
		return nil, fmt.Errorf("lifecycle: pipeline %s: at least one stage is required", cfg.Controller)
	}
	seen := make(map[string]struct{}, len(cfg.Stages))
	for i, stage := range cfg.Stages {
		if stage.Name == "" {
			return nil, fmt.Errorf("lifecycle: pipeline %s: stage %d has no name", cfg.Controller, i)
		}
		if stage.Run == nil {
			return nil, fmt.Errorf("lifecycle: pipeline %s: stage %s has no Run", cfg.Controller, stage.Name)
		}
		if _, dup := seen[stage.Name]; dup {
			return nil, fmt.Errorf("lifecycle: pipeline %s: stage %s registered twice", cfg.Controller, stage.Name)
		}
		seen[stage.Name] = struct{}{}
	}
	cfg.Stages = append([]Stage[S](nil), cfg.Stages...)
	if cfg.Locks == nil {
		cfg.Locks = &KeyedLock{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	registerMetrics()
	return &Pipeline[S]{cfg: cfg}, nil
}

// StageNames lists the registered stages in execution order.
func (p *Pipeline[S]) StageNames() []string {
	names := make([]string, len(p.cfg.Stages))
	for i, stage := range p.cfg.Stages {
		names[i] = stage.Name
	}
	return names
}

// Reconcile acquires the object's lock and runs the stages in order until one
// returns Done or an error, or the chain ends.
func (p *Pipeline[S]) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	key := req.String()
	unlock, err := p.cfg.Locks.Lock(ctx, key)
	if err != nil {
		return reconcile.Result{}, err
	}
	defer unlock()

	state := p.cfg.NewState(req)
	for _, stage := range p.cfg.Stages {
		if err := ctx.Err(); err != nil {
			return reconcile.Result{}, fmt.Errorf("%s %s: before stage %s: %w", p.cfg.Controller, key, stage.Name, err)
		}
		step, err := p.runStage(ctx, stage, key, state)
		if err != nil {
			wrapped := fmt.Errorf("%s %s: stage %s: %w", p.cfg.Controller, key, stage.Name, err)
			if stage.FailureReason != "" && p.cfg.OnFailure != nil {
				if ferr := p.cfg.OnFailure(ctx, state, stage.FailureReason, err); ferr != nil {
					wrapped = errors.Join(wrapped, fmt.Errorf("record %s condition: %w", stage.FailureReason, ferr))
				}
			}
			return reconcile.Result{}, wrapped
		}
		if step.stop {
			return step.result, nil
		}
	}
	return reconcile.Result{}, nil
}

func (p *Pipeline[S]) runStage(ctx context.Context, stage Stage[S], key string, state *S) (step Step, err error) {
	controller := p.cfg.Controller
	if p.cfg.Observer != nil {
		p.cfg.Observer.StageStarted(ctx, controller, stage.Name, key)
	}
	started := time.Now()
	outcome := OutcomeContinue
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = OutcomePanic
			step = Step{}
			err = fmt.Errorf("panic: %v", recovered)
		}
		elapsed := time.Since(started)
		stageTotal.WithLabelValues(controller, stage.Name, string(outcome)).Inc()
		stageDuration.WithLabelValues(controller, stage.Name).Observe(elapsed.Seconds())
		if err != nil {
			p.cfg.Log.WarnContext(ctx, "operator_stage_failed", "controller", controller, "stage", stage.Name, "key", key, "outcome", outcome, "duration_ms", elapsed.Milliseconds(), "error", err)
		} else {
			p.cfg.Log.DebugContext(ctx, "operator_stage_completed", "controller", controller, "stage", stage.Name, "key", key, "outcome", outcome, "duration_ms", elapsed.Milliseconds())
		}
		if p.cfg.Observer != nil {
			p.cfg.Observer.StageFinished(ctx, controller, stage.Name, key, outcome, elapsed)
		}
	}()
	step, err = stage.Run(ctx, state)
	switch {
	case err != nil:
		outcome = OutcomeError
	case step.stop:
		outcome = OutcomeDone
	}
	return step, err
}

var (
	stageTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "astronomer",
		Subsystem: "operator",
		Name:      "stage_total",
		Help:      "Reconcile stage executions by controller, stage and outcome (continue, done, error, panic).",
	}, []string{"controller", "stage", "outcome"})
	stageDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "astronomer",
		Subsystem: "operator",
		Name:      "stage_duration_seconds",
		Help:      "Reconcile stage latency by controller and stage.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"controller", "stage"})
	metricsOnce sync.Once
)

// registerMetrics adds the stage collectors to the default registry, which
// the server exposes on SERVER_METRICS_ADDR. A collector some other code
// already registered is tolerated.
func registerMetrics() {
	metricsOnce.Do(func() {
		for _, collector := range []prometheus.Collector{stageTotal, stageDuration} {
			if err := prometheus.Register(collector); err != nil {
				var already prometheus.AlreadyRegisteredError
				if !errors.As(err, &already) {
					panic(fmt.Sprintf("lifecycle: register stage metrics: %v", err))
				}
			}
		}
	})
}
