//go:build integration

package registry_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/registry"
	"github.com/alphabravocompany/astronomer-go/internal/testzot"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newResolver(t *testing.T, local string, fallbacks ...string) (*registry.Resolver, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	plain := append([]string{local}, fallbacks...)
	r, err := registry.New(registry.Config{Local: local, Fallbacks: fallbacks, PlainHTTP: plain}, registry.VerifyDigestPinned, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return r, logs
}

// resolveExpect resolves ref and checks the layer, the logged layer=<name>
// line and (for hits) the resolved reference prefix.
func resolveExpect(t *testing.T, r *registry.Resolver, logs *syncBuffer, ref, wantLayer, wantPrefix string) registry.Resolved {
	t.Helper()
	got, err := r.Resolve(context.Background(), ref)
	if wantLayer == registry.LayerFailClosed {
		if !errors.Is(err, registry.ErrFailClosed) {
			t.Fatalf("expected fail closed for %s, got %+v %v", ref, got, err)
		}
	} else if err != nil || got.Layer != wantLayer || !strings.HasPrefix(got.Ref, wantPrefix) {
		t.Fatalf("Resolve(%s) = %+v, %v; want layer %s prefix %s", ref, got, err, wantLayer, wantPrefix)
	}
	if !strings.Contains(logs.String(), "layer="+wantLayer) {
		t.Fatalf("missing layer=%s log: %s", wantLayer, logs)
	}
	return got
}

func TestResolverAgainstZot(t *testing.T) {
	t.Parallel()
	local := testzot.Start(t)
	fallback := testzot.Start(t)
	localDesc := testzot.PushImage(t, testzot.Repository(t, local+"/alphabravo-oss/astronomer-go-agent"), "v2.0.1", "agent")
	fallbackDesc := testzot.PushImage(t, testzot.Repository(t, fallback+"/fluxcd/helm-controller"), "v1.0.0", "flux")
	pinned := "ghcr.io/fluxcd/helm-controller@" + fallbackDesc.Digest.String()

	t.Run("local hit", func(t *testing.T) {
		t.Parallel()
		r, logs := newResolver(t, local)
		got := resolveExpect(t, r, logs, "ghcr.io/alphabravo-oss/astronomer-go-agent:v2.0.1", registry.LayerLocal, local+"/alphabravo-oss/astronomer-go-agent:v2.0.1")
		if got.Digest != localDesc.Digest.String() {
			t.Fatalf("digest = %s, want %s", got.Digest, localDesc.Digest)
		}
	})

	t.Run("fallback disabled fails closed", func(t *testing.T) {
		t.Parallel()
		r, logs := newResolver(t, local)
		resolveExpect(t, r, logs, pinned, registry.LayerFailClosed, "")
		if strings.Contains(logs.String(), "layer=fallback") {
			t.Fatalf("fallback consulted although none is configured: %s", logs)
		}
	})

	t.Run("fallback hit when opted in", func(t *testing.T) {
		t.Parallel()
		r, logs := newResolver(t, local, fallback)
		resolveExpect(t, r, logs, pinned, registry.LayerFallback, fallback+"/fluxcd/helm-controller@")
	})

	t.Run("unauthenticated fallback fails closed", func(t *testing.T) {
		t.Parallel()
		r, logs := newResolver(t, local, fallback)
		// Tag-only: the fallback holds it, but nothing signed pins its digest.
		resolveExpect(t, r, logs, "ghcr.io/fluxcd/helm-controller:v1.0.0", registry.LayerFailClosed, "")
		if !strings.Contains(logs.String(), "not digest-pinned") {
			t.Fatalf("verifier refusal not logged: %s", logs)
		}
	})
}
