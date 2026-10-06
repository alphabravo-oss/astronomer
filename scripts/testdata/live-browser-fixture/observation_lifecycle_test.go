package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFixtureSharesNegotiatedObservationComposition(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(source)
	start := strings.Index(body, "func runAgent() error {")
	end := strings.Index(body[start:], "\ntype k8sFixture struct")
	body = body[start : start+end]
	for _, want := range []string{
		"metadata.NewForConfig(restConfig)",
		"restConfig = proxy.RESTConfig()",
		"agentdelivery.NewClusterProbeForConfig(proxy.Client(), restConfig, true)",
		"subscriber := agent.NewStateSubscriber(proxy.Client(), client, log)",
		"subscriber.SetMetadataClient(metadataClient)",
		"subscriber.SetConnectionWatcher(client)",
		"deliveryProbe.WithDynamicClient(deliveryDynamic).WithObservationSource(subscriber)",
		"agent.NewObservedDeliveryRuntime(",
		"ObservationFreshness: client.ObservationFreshnessEnabled",
		"}, deliveryDynamic, deliveryStore, deliveryProbe)",
		"client.SetObservationRetry(deliveryRuntime.RetryObservation)",
		"runFixtureObservers(ctx, client.Connect, subscriber.Run, mirror.Run,",
		"deliveryRuntime.Run(runCtx, client.SendFunc(runCtx))",
	} {
		if strings.Count(body, want) != 1 {
			t.Errorf("expected exactly one shared composition edge %q", want)
		}
	}
	if strings.Contains(body, "agentdelivery.NewRuntime(") {
		t.Fatal("fixture bypasses observed runtime constructor")
	}
	if strings.Index(body, "WithObservationSource(subscriber)") > strings.Index(body, "runFixtureObservers(") {
		t.Fatal("observation source injected after start")
	}
}

func TestFixtureObservationLifecycleJoinsAllWorkers(t *testing.T) {
	for _, reason := range []string{"connect_error", "parent_cancel", "runtime_error", "runtime_return"} {
		t.Run(reason, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{}, 3)
			release := make(chan struct{})
			var finished atomic.Int32
			worker := func(ctx context.Context) { started <- struct{}{}; <-ctx.Done(); <-release; finished.Add(1) }
			failure := errors.New("fixture failure")
			trigger := make(chan struct{})
			delivery := func(ctx context.Context) error {
				started <- struct{}{}
				if reason == "runtime_error" || reason == "runtime_return" {
					<-trigger
					finished.Add(1)
					if reason == "runtime_error" {
						return failure
					}
					return nil
				}
				<-ctx.Done()
				<-release
				finished.Add(1)
				return ctx.Err()
			}
			connect := func(ctx context.Context) error {
				if reason == "connect_error" {
					<-trigger
					return failure
				}
				<-ctx.Done()
				return ctx.Err()
			}
			done := make(chan error, 1)
			go func() { done <- runFixtureObservers(ctx, connect, worker, worker, delivery) }()
			for i := 0; i < 3; i++ {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("worker did not start")
				}
			}
			if reason == "parent_cancel" {
				cancel()
			} else {
				close(trigger)
			}
			select {
			case <-done:
				t.Fatal("returned before joining workers")
			case <-time.After(10 * time.Millisecond):
			}
			close(release)
			select {
			case err := <-done:
				if finished.Load() != 3 {
					t.Fatal("workers not joined")
				}
				if (reason == "connect_error" || reason == "runtime_error") && !errors.Is(err, failure) {
					t.Fatalf("lost failure: %v", err)
				}
				if reason == "parent_cancel" && !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
				if reason == "runtime_return" && err == nil {
					t.Fatal("unexpected runtime stop ignored")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("workers did not stop")
			}
		})
	}
}
