package helmruntime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

// OperationContext supplies one deadline for storage, discovery, owned chart
// transfers and waits. Every network adapter must bind this context separately.
// An earlier caller deadline remains authoritative; callers must retain cancel
// until the joined SDK action has returned.
func OperationContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	if parent == nil || timeout <= 0 {
		return nil, nil, errors.New("finite Helm operation context required")
	}
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	owned, cancel := context.WithTimeout(parent, timeout)
	return owned, cancel, nil
}

// RequestClientGetter creates a request-local Kubernetes configuration. Storage,
// discovery and REST mapping must all use its bound transport; delegating the
// latter two to the original getter would silently bypass cancellation.
func RequestClientGetter(ctx context.Context, base genericclioptions.RESTClientGetter) (genericclioptions.RESTClientGetter, error) {
	if ctx == nil || base == nil {
		return nil, errors.New("owned Helm request context/getter required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &requestGetter{ctx: ctx, base: base}, nil
}

type requestGetter struct {
	ctx            context.Context
	base           genericclioptions.RESTClientGetter
	discoveryOnce  sync.Once
	discovery      discovery.CachedDiscoveryInterface
	discoveryError error
}

func (g *requestGetter) ToRESTConfig() (*rest.Config, error) {
	if err := g.ctx.Err(); err != nil {
		return nil, err
	}
	source, err := g.base.ToRESTConfig()
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, errors.New("Helm Kubernetes config unavailable")
	}
	cfg := rest.CopyConfig(source)
	previous := cfg.WrapTransport
	cfg.WrapTransport = func(transport http.RoundTripper) http.RoundTripper {
		if previous != nil {
			transport = previous(transport)
		}
		return &requestTransport{owner: g.ctx, next: transport}
	}
	if deadline, ok := g.ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, context.DeadlineExceeded
		}
		if cfg.Timeout <= 0 || cfg.Timeout > remaining {
			cfg.Timeout = remaining
		}
	}
	return cfg, nil
}
func (g *requestGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	if err := g.ctx.Err(); err != nil {
		return nil, err
	}
	g.discoveryOnce.Do(func() {
		cfg, err := g.ToRESTConfig()
		if err != nil {
			g.discoveryError = err
			return
		}
		client, err := discovery.NewDiscoveryClientForConfig(cfg)
		if err != nil {
			g.discoveryError = err
			return
		}
		g.discovery = memory.NewMemCacheClient(client)
	})
	return g.discovery, g.discoveryError
}
func (g *requestGetter) ToRESTMapper() (meta.RESTMapper, error) {
	discovery, err := g.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}
	return restmapper.NewShortcutExpander(restmapper.NewDeferredDiscoveryRESTMapper(discovery), discovery, nil), nil
}
func (g *requestGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return requestLoader{ClientConfig: g.base.ToRawKubeConfigLoader(), getter: g}
}

type requestLoader struct {
	clientcmd.ClientConfig
	getter *requestGetter
}

func (loader requestLoader) ClientConfig() (*rest.Config, error) { return loader.getter.ToRESTConfig() }

// HTTPClient retains a caller's transport, TLS, redirects and cookie policy,
// while binding every request and body read to the owned operation context.
// It also covers registry APIs that construct their own background contexts.
func HTTPClient(ctx context.Context, base *http.Client) (*http.Client, error) {
	if ctx == nil {
		return nil, errors.New("owned Helm HTTP context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	next := base.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	client.Transport = &requestTransport{owner: ctx, next: next}
	return &client, nil
}

type requestTransport struct {
	owner context.Context
	next  http.RoundTripper
}

func (transport *requestTransport) CloseIdleConnections() {
	if next, ok := transport.next.(interface{ CloseIdleConnections() }); ok {
		next.CloseIdleConnections()
	}
}

// Both cancellation domains remain active. The SDK's own request context may
// be narrower than the operation context; owner values fill absent request
// values (including trace correlation) without discarding SDK values.
type joinedRequestContext struct {
	context.Context
	owner context.Context
}

func (ctx joinedRequestContext) Value(key any) any {
	if value := ctx.Context.Value(key); value != nil {
		return value
	}
	return ctx.owner.Value(key)
}
func (transport *requestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := transport.owner.Err(); err != nil {
		return nil, err
	}
	parent := request.Context()
	deadlineStop := func() {}
	if deadline, ok := transport.owner.Deadline(); ok {
		parent, deadlineStop = context.WithDeadline(parent, deadline)
	}
	joined, cancel := context.WithCancelCause(parent)
	stopOwner := context.AfterFunc(transport.owner, func() { cancel(context.Cause(transport.owner)) })
	var once sync.Once
	release := func() { once.Do(func() { stopOwner(); cancel(nil); deadlineStop() }) }
	if err := transport.owner.Err(); err != nil {
		cancel(context.Cause(transport.owner))
	}
	response, err := transport.next.RoundTrip(request.Clone(joinedRequestContext{Context: joined, owner: transport.owner}))
	if err != nil {
		release()
		return response, err
	}
	if response == nil {
		release()
		return nil, errors.New("Helm Kubernetes transport returned no response")
	}
	if response.Body == nil {
		release()
	} else {
		response.Body = &ownedResponseBody{ReadCloser: response.Body, release: release}
	}
	return response, nil
}

type ownedResponseBody struct {
	io.ReadCloser
	release func()
}

func (body *ownedResponseBody) Read(buffer []byte) (int, error) {
	count, err := body.ReadCloser.Read(buffer)
	if err != nil {
		body.release()
	}
	return count, err
}
func (body *ownedResponseBody) Close() error {
	err := body.ReadCloser.Close()
	body.release()
	return err
}
