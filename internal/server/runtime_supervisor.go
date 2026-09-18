package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// runtimeSupervisor owns the process-lifetime goroutines started by the API
// server. A loop registered with Go must block until its context is cancelled.
// Returning before cancellation is an unexpected exit; critical exits fail
// readiness and wake Server.Start so the process can shut down in order.
type runtimeSupervisor struct {
	log *slog.Logger

	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	started  bool
	stopping bool
	loops    map[string]runtimeLoopState
	failure  error
	failures chan error
	wg       sync.WaitGroup
}

type runtimeLoopState struct {
	critical bool
	finite   bool
	running  bool
	err      error
}

func newRuntimeSupervisor(log *slog.Logger) *runtimeSupervisor {
	if log == nil {
		log = slog.Default()
	}
	return &runtimeSupervisor{
		log:      log,
		loops:    make(map[string]runtimeLoopState),
		failures: make(chan error, 1),
	}
}

func (s *runtimeSupervisor) Start(parent context.Context) error {
	if s == nil {
		return errors.New("runtime supervisor is nil")
	}
	if parent == nil {
		parent = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return errors.New("runtime supervisor already started")
	}
	s.ctx, s.cancel = context.WithCancel(parent)
	s.started = true
	return nil
}

// Go registers and starts one process-lifetime loop. Names are unique so
// readiness output and shutdown errors identify the exact owner.
func (s *runtimeSupervisor) Go(name string, critical bool, run func(context.Context) error) error {
	return s.start(name, critical, false, run)
}

// Task owns a finite startup/background task. Its completion is expected, but
// it still participates in the shutdown join if cancellation races the task.
func (s *runtimeSupervisor) Task(name string, run func(context.Context) error) error {
	return s.start(name, false, true, run)
}

func (s *runtimeSupervisor) start(name string, critical, finite bool, run func(context.Context) error) error {
	if s == nil || run == nil {
		return errors.New("runtime loop and supervisor are required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("runtime loop name is required")
	}
	s.mu.Lock()
	if !s.started || s.ctx == nil {
		s.mu.Unlock()
		return fmt.Errorf("register runtime loop %q before supervisor start", name)
	}
	if s.stopping {
		s.mu.Unlock()
		return fmt.Errorf("register runtime loop %q during shutdown", name)
	}
	if _, exists := s.loops[name]; exists {
		s.mu.Unlock()
		return fmt.Errorf("runtime loop %q is already registered", name)
	}
	ctx := s.ctx
	s.loops[name] = runtimeLoopState{critical: critical, finite: finite, running: true}
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		err := run(ctx)
		s.loopExited(name, err, ctx.Err() != nil)
	}()
	return nil
}

func (s *runtimeSupervisor) GoLoop(name string, critical bool, run func(context.Context)) error {
	if run == nil {
		return errors.New("runtime loop is required")
	}
	return s.Go(name, critical, func(ctx context.Context) error {
		run(ctx)
		return nil
	})
}

func (s *runtimeSupervisor) loopExited(name string, err error, canceled bool) {
	s.mu.Lock()
	state, exists := s.loops[name]
	if !exists {
		s.mu.Unlock()
		return
	}
	state.running = false
	intentional := s.stopping || canceled || state.finite
	publishFailure := false
	if !intentional {
		if err == nil {
			err = errors.New("loop exited without cancellation")
		}
		state.err = err
		if state.critical && s.failure == nil {
			s.failure = fmt.Errorf("critical runtime %s: %w", name, err)
			publishFailure = true
		}
	}
	s.loops[name] = state
	failure := s.failure
	s.mu.Unlock()

	if intentional {
		s.log.Debug("runtime loop stopped", "component", name)
		return
	}
	s.log.Error("runtime loop exited unexpectedly", "component", name, "critical", state.critical, "error", err)
	if publishFailure && failure != nil {
		select {
		case s.failures <- failure:
		default:
		}
	}
}

func (s *runtimeSupervisor) Started() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

func (s *runtimeSupervisor) Healthy() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started && !s.stopping && s.failure == nil
}

func (s *runtimeSupervisor) HealthError() string {
	if s == nil {
		return "runtime supervisor is unavailable"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case !s.started:
		return "runtime supervisor has not started"
	case s.stopping:
		return "runtime shutdown is in progress"
	case s.failure != nil:
		return s.failure.Error()
	default:
		return ""
	}
}

func (s *runtimeSupervisor) Failures() <-chan error {
	if s == nil {
		return nil
	}
	return s.failures
}

// BeginStop marks all subsequent exits intentional and broadcasts
// cancellation. Resources with independent intake controls (for example
// asynq.Server) can then be stopped before Wait joins the loops.
func (s *runtimeSupervisor) MarkStopping() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopping {
		return
	}
	s.stopping = true
}

func (s *runtimeSupervisor) BeginStop() {
	if s == nil {
		return
	}
	s.MarkStopping()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

// Stop cancels every owned loop and waits for all completion signals. The
// caller supplies the process shutdown deadline; a hung loop is reported with
// its name and dependencies remain open so the error is diagnosable.
func (s *runtimeSupervisor) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.BeginStop()
	return s.Wait(ctx)
}

func (s *runtimeSupervisor) Wait(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		var running []string
		for name, state := range s.loops {
			if state.running {
				running = append(running, name)
			}
		}
		s.mu.Unlock()
		sort.Strings(running)
		return fmt.Errorf("join runtime loops [%s]: %w", strings.Join(running, ", "), ctx.Err())
	}
}
