package main

import (
	"context"
	"errors"
	"sync"
)

// runFixtureObservers owns the fixture's observation lifetime, independent of
// tunnel reconnects. The delivery runtime in turn owns and joins its Flux cache.
// This fixture still supplies synthetic CIS/Loki edges; it is not live-cluster
// qualification evidence.
func runFixtureObservers(parent context.Context, connect func(context.Context) error, subscriber, mirror func(context.Context), delivery func(context.Context) error) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var workers sync.WaitGroup
	workers.Add(3)
	go func() { defer workers.Done(); subscriber(ctx) }()
	go func() { defer workers.Done(); mirror(ctx) }()
	var deliveryErr error
	go func() {
		defer workers.Done()
		err := delivery(ctx)
		if ctx.Err() == nil {
			deliveryErr = err
			if err == nil {
				deliveryErr = errors.New("fixture delivery runtime stopped unexpectedly")
			}
			cancel()
		}
	}()
	connectErr := connect(ctx)
	cancel()
	workers.Wait()
	if deliveryErr != nil {
		return deliveryErr
	}
	return connectErr
}
