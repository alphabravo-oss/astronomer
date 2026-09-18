package server

import (
	"context"
	"errors"
	"fmt"
)

type namedRuntimeLoop struct {
	name string
	run  func(context.Context)
}

// runRuntimeLoopGroup gives leader-scoped loops one cancellation source and
// one joined completion signal. If any child exits while leadership is still
// active, the whole group stops and reports the exact child as failed.
func runRuntimeLoopGroup(parent context.Context, loops ...namedRuntimeLoop) error {
	if len(loops) == 0 {
		<-parent.Done()
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	type result struct {
		name string
	}
	results := make(chan result, len(loops))
	for _, loop := range loops {
		loop := loop
		go func() {
			loop.run(ctx)
			results <- result{name: loop.name}
		}()
	}

	first := <-results
	if parent.Err() == nil {
		cancel()
		for range len(loops) - 1 {
			<-results
		}
		return fmt.Errorf("runtime loop %s: %w", first.name, errors.New("exited without cancellation"))
	}
	for range len(loops) - 1 {
		<-results
	}
	return nil
}
