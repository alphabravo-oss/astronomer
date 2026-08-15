package charlie

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ActivationWork is one generation of Charlie's product work plane. A
// generation is constructed only while the single activation gate is runnable
// and must release every listener, consumer, and client when stopped.
type ActivationWork interface {
	Run(context.Context)
	Shutdown(context.Context) error
}

type ActivationWorkFactory func(context.Context) (ActivationWork, error)

// RuntimeLifecycle owns dynamic Charlie work-plane generations. It has no
// polling goroutine while inactive: feature enable and signed mode transitions
// explicitly wake it, while an active generation watches for authority loss.
type RuntimeLifecycle struct {
	features       featureReader
	queries        activeConnectionReader
	factory        ActivationWorkFactory
	eligible       func(Activation) bool
	control        func(context.Context)
	closeTransport func()
	ticker         func(time.Duration) runtimeTicker
	interval       time.Duration

	transition sync.Mutex
	mu         sync.Mutex
	parent     context.Context
	work       ActivationWork
	generation context.CancelFunc
}

func NewRuntimeLifecycle(features featureReader, queries activeConnectionReader, factory ActivationWorkFactory, control func(context.Context), closeTransport func()) (*RuntimeLifecycle, error) {
	return newRuntimeLifecycle(features, queries, factory, func(activation Activation) bool { return activation.Runnable }, control, closeTransport)
}

// NewConfigurationRuntimeLifecycle owns the product-local configuration
// surface and signed authority reconciliation. It may exist for an installed,
// operationally disabled connection so the product agent can rediscover the
// exact MCP catalog and import a reviewed disclosure, but it remains absent for
// feature-off, unconfigured, installing, and emergency-stop states.
func NewConfigurationRuntimeLifecycle(features featureReader, queries activeConnectionReader, factory ActivationWorkFactory, control func(context.Context), closeTransport func()) (*RuntimeLifecycle, error) {
	return newRuntimeLifecycle(features, queries, factory, configurationDiscoveryAllowed, control, closeTransport)
}

func newRuntimeLifecycle(features featureReader, queries activeConnectionReader, factory ActivationWorkFactory, eligible func(Activation) bool, control func(context.Context), closeTransport func()) (*RuntimeLifecycle, error) {
	if features == nil || queries == nil || factory == nil || eligible == nil {
		return nil, fmt.Errorf("Charlie runtime lifecycle requires activation state and a work factory")
	}
	return &RuntimeLifecycle{
		features: features, queries: queries, factory: factory, eligible: eligible, control: control,
		closeTransport: closeTransport, ticker: newRuntimeTicker, interval: 500 * time.Millisecond,
	}, nil
}

// Start records the process lifetime and materializes a work generation only
// when authority is already runnable. Inactive startup creates no timer.
func (l *RuntimeLifecycle) Start(parent context.Context) error {
	if l == nil {
		return nil
	}
	if parent == nil {
		parent = context.Background()
	}
	l.mu.Lock()
	l.parent = parent
	l.mu.Unlock()
	return l.Activate(parent)
}

// Activate re-evaluates the canonical gate and starts a fresh generation when
// a feature or signed mode transition grants work authority.
func (l *RuntimeLifecycle) Activate(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.transition.Lock()
	defer l.transition.Unlock()
	activation := EvaluateActivation(ctx, l.features, l.queries)
	if !l.eligible(activation) {
		return l.stopGeneration(ctx, nil)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.work != nil {
		return nil
	}
	parent := l.parent
	if parent == nil {
		parent = context.Background()
	}
	work, err := l.factory(ctx)
	if err != nil {
		return err
	}
	// Construction can span trust and client setup. Recheck before publishing
	// the generation so a concurrent disable cannot leave it runnable.
	if !l.eligible(EvaluateActivation(ctx, l.features, l.queries)) {
		_ = work.Shutdown(ctx)
		if l.closeTransport != nil {
			l.closeTransport()
		}
		return nil
	}
	generationCtx, cancel := context.WithCancel(parent)
	l.work, l.generation = work, cancel
	go work.Run(generationCtx)
	if l.control != nil {
		go l.control(generationCtx)
	}
	go l.watch(generationCtx, work)
	return nil
}

func (l *RuntimeLifecycle) watch(ctx context.Context, generation ActivationWork) {
	ticker := l.ticker(l.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			if !l.eligible(EvaluateActivation(ctx, l.features, l.queries)) {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				l.transition.Lock()
				_ = l.stopGeneration(shutdownCtx, generation)
				l.transition.Unlock()
				cancel()
				return
			}
		}
	}
}

// Shutdown is reversible: it quiesces the current generation but retains the
// process parent so a later authorized Enable can call Activate without a
// process restart.
func (l *RuntimeLifecycle) Shutdown(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.transition.Lock()
	defer l.transition.Unlock()
	return l.stopGeneration(ctx, nil)
}

func (l *RuntimeLifecycle) stopGeneration(ctx context.Context, expected ActivationWork) error {
	l.mu.Lock()
	if expected != nil && l.work != expected {
		l.mu.Unlock()
		return nil
	}
	work, cancel := l.work, l.generation
	l.work, l.generation = nil, nil
	if cancel != nil {
		cancel()
	}
	l.mu.Unlock()

	var err error
	if work != nil {
		err = work.Shutdown(ctx)
	}
	if l.closeTransport != nil {
		l.closeTransport()
	}
	return err
}
