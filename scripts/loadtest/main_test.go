package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestSyntheticAgentRampDeadlineBoundsSaturatedHandshakeBatch(t *testing.T) {
	const concurrency = 2
	agents := make([]*syntheticAgent, concurrency+1)
	for index := range agents {
		agents[index] = &syntheticAgent{ready: make(chan struct{})}
	}

	lifetimeCtx, stopAgents := context.WithCancel(context.Background())
	var agentWG sync.WaitGroup
	rampCtx, cancelRamp := context.WithTimeout(context.Background(), 25*time.Millisecond)
	started := time.Now()
	err := runSyntheticAgentRamp(rampCtx, lifetimeCtx, agents, concurrency, &agentWG, func(ctx context.Context, _ *syntheticAgent) {
		<-ctx.Done()
	})
	cancelRamp()
	stopAgents()
	agentWG.Wait()

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runSyntheticAgentRamp() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("runSyntheticAgentRamp() returned after %v; saturated ramp ignored its deadline", elapsed)
	}
}

func TestSyntheticConnectCapabilitiesCoverAdmissionAndReadTraffic(t *testing.T) {
	capabilities := make(map[string]struct{})
	for _, capability := range syntheticConnectCapabilities() {
		capabilities[capability] = struct{}{}
	}
	for _, required := range append(protocol.RequiredConnectCapabilities(), "watch") {
		if _, found := capabilities[required]; !found {
			t.Fatalf("synthetic CONNECT is missing %q", required)
		}
	}
}
