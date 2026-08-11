package server

import (
	"context"
	"sync"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/hibiken/asynq"
)

// charlieRuntimeGeneration is the dynamically replaceable product work plane.
// It owns only event consumption and trigger registration; private Product MCP
// configuration discovery has its own stricter lifecycle below.
type charlieRuntimeGeneration struct {
	events     *charlie.EventRuntime
	dispatcher tasks.CharlieTriggerDispatcher

	mu      sync.Mutex
	stopped bool
}

func (g *charlieRuntimeGeneration) Run(ctx context.Context) {
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		return
	}
	tasks.ConfigureCharlieTriggerDispatcher(g.dispatcher)
	g.mu.Unlock()
	if g.events != nil {
		go g.events.Run(ctx)
	}
	<-ctx.Done()
}

func (g *charlieRuntimeGeneration) Shutdown(ctx context.Context) error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		return nil
	}
	g.stopped = true
	tasks.ConfigureCharlieTriggerDispatcher(nil)
	g.mu.Unlock()
	return nil
}

// charlieMCPGeneration owns the private configuration-discovery listener and
// its capability-adapter resources. It never registers trigger or event work.
type charlieMCPGeneration struct {
	mcp       *charlie.MCPRuntime
	inspector *asynq.Inspector
	mu        sync.Mutex
	stopped   bool
}

// charlieLifecycleGroup keeps configuration discovery and runtime work as two
// independent gates while presenting one atomic lifecycle to feature control
// and server shutdown.
type charlieLifecycleGroup struct {
	configuration *charlie.RuntimeLifecycle
	runtime       *charlie.RuntimeLifecycle
}

func (g *charlieLifecycleGroup) Start(ctx context.Context) error {
	if g == nil {
		return nil
	}
	if g.configuration != nil {
		if err := g.configuration.Start(ctx); err != nil {
			return err
		}
	}
	if g.runtime != nil {
		return g.runtime.Start(ctx)
	}
	return nil
}

func (g *charlieLifecycleGroup) Activate(ctx context.Context) error {
	if g == nil {
		return nil
	}
	if g.configuration != nil {
		if err := g.configuration.Activate(ctx); err != nil {
			return err
		}
	}
	if g.runtime != nil {
		return g.runtime.Activate(ctx)
	}
	return nil
}

func (g *charlieLifecycleGroup) Shutdown(ctx context.Context) error {
	if g == nil {
		return nil
	}
	var first error
	if g.runtime != nil {
		first = g.runtime.Shutdown(ctx)
	}
	if g.configuration != nil {
		if err := g.configuration.Shutdown(ctx); first == nil {
			first = err
		}
	}
	return first
}

func (g *charlieMCPGeneration) Run(ctx context.Context) {
	if g == nil || g.mcp == nil {
		return
	}
	_ = g.mcp.Run(ctx)
}

func (g *charlieMCPGeneration) Shutdown(ctx context.Context) error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		return nil
	}
	g.stopped = true
	g.mu.Unlock()
	var err error
	if g.mcp != nil {
		err = g.mcp.Shutdown(ctx)
	}
	if g.inspector != nil {
		_ = g.inspector.Close()
	}
	return err
}
