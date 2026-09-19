package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

func TestLocalFluxBootstrapSupportsEveryPinnedDistributionObject(t *testing.T) {
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewBufferString(fluxdistribution.InstallYAML()), 64<<10)
	objects := 0
	for {
		var object unstructured.Unstructured
		err := decoder.Decode(&object)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode distribution: %v", err)
		}
		if object.GetKind() == "" {
			continue
		}
		objects++
		if _, ok := localFluxResources[object.GroupVersionKind()]; !ok {
			t.Fatalf("unsupported bootstrap object %s %s/%s", object.GroupVersionKind(), object.GetNamespace(), object.GetName())
		}
	}
	if objects == 0 {
		t.Fatal("embedded Flux distribution is empty")
	}
}

func TestLocalFluxBootstrapRetriesTransientFailuresUntilApplied(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	attempts := 0
	err := retryLocalFluxBootstrap(context.Background(), logger, time.Millisecond, func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary Kubernetes API failure")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retry bootstrap: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("bootstrap attempts = %d, want 3", attempts)
	}
}

func TestLocalFluxBootstrapStopsCleanlyOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	attempts := 0
	err := retryLocalFluxBootstrap(ctx, logger, time.Hour, func(context.Context) error {
		attempts++
		cancel()
		return errors.New("temporary Kubernetes API failure")
	})
	if err != nil {
		t.Fatalf("shutdown should stop retry cleanly: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("bootstrap attempts = %d, want 1", attempts)
	}
}
