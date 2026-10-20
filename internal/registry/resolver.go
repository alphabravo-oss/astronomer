package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	orasregistry "oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
	"oras.land/oras-go/v2/registry/remote/retry"
)

// Layer names logged as layer=<name> for every resolution.
const (
	LayerLocal      = "local"
	LayerFallback   = "fallback"
	LayerFailClosed = "fail_closed"
	// LayerOnline is the explicit online profile: no cluster-local registry is
	// configured, so references are used as rendered. It is never selected
	// when REGISTRY_LOCAL is set.
	LayerOnline = "online"
)

// ErrFailClosed is returned when no configured layer holds the reference.
var ErrFailClosed = errors.New("registry resolution failed closed")

// VerifyFunc authenticates a fallback hit before it is used. original is the
// reference as rendered (from the signed release/bundle), candidate the
// fallback reference and desc the manifest descriptor the fallback served.
type VerifyFunc func(ctx context.Context, original, candidate string, desc ocispec.Descriptor) error

// Resolved is the outcome of one resolution.
type Resolved struct {
	Ref    string
	Layer  string
	Digest string
}

// Resolver applies the layered policy. Local is the cluster-local registry
// host (REGISTRY_LOCAL); Fallbacks the opt-in layer-2 list
// (REGISTRY_FALLBACKS); Verify the mandatory fallback authenticator.
type Resolver struct {
	Local     string
	Fallbacks []string
	Verify    VerifyFunc
	// PlainHTTP lists registry hosts reached over plain HTTP (an in-cluster
	// registry without TLS). Every other host uses HTTPS.
	PlainHTTP map[string]bool
	Client    remote.Client
	Logger    *slog.Logger
	// Timeout bounds each manifest HEAD; zero means 10s.
	Timeout time.Duration
}

// Config is the process configuration for a Resolver.
type Config struct {
	Local     string
	Fallbacks []string
	PlainHTTP []string
}

// New validates cfg and returns a Resolver using the docker credential store
// when one exists. A non-empty fallback list without a verifier is refused:
// layer 2 must carry the same signed-content discipline as layer 1.
func New(cfg Config, verify VerifyFunc, logger *slog.Logger) (*Resolver, error) {
	if cfg.Local != "" {
		if err := ValidateBase(cfg.Local); err != nil {
			return nil, fmt.Errorf("REGISTRY_LOCAL: %w", err)
		}
	}
	for _, fallback := range cfg.Fallbacks {
		if err := ValidateBase(fallback); err != nil {
			return nil, fmt.Errorf("REGISTRY_FALLBACKS: %w", err)
		}
	}
	if len(cfg.Fallbacks) > 0 && cfg.Local == "" {
		return nil, errors.New("REGISTRY_FALLBACKS requires REGISTRY_LOCAL: fallbacks are layer 2 behind the cluster-local registry")
	}
	if len(cfg.Fallbacks) > 0 && verify == nil {
		return nil, errors.New("REGISTRY_FALLBACKS requires a fallback verifier")
	}
	if logger == nil {
		logger = slog.Default()
	}
	plain := map[string]bool{}
	for _, host := range cfg.PlainHTTP {
		if host = strings.TrimSpace(host); host != "" {
			plain[host] = true
		}
	}
	return &Resolver{Local: cfg.Local, Fallbacks: cfg.Fallbacks, Verify: verify, PlainHTTP: plain, Client: DefaultClient(), Logger: logger}, nil
}

// DefaultClient returns an oras client with retry and the ambient docker
// credential store (anonymous when none is configured).
func DefaultClient() *auth.Client {
	client := &auth.Client{Client: retry.DefaultClient, Cache: auth.NewCache()}
	if store, err := credentials.NewStoreFromDocker(credentials.StoreOptions{}); err == nil {
		client.Credential = credentials.Credential(store)
	}
	return client
}

// VerifyDigestPinned is the default fallback verifier: the original
// reference must be digest-pinned (its digest comes from the signed release
// manifest or signed bundle catalog) and the fallback must serve exactly that
// manifest digest. A tag-only reference cannot be authenticated and is refused.
func VerifyDigestPinned(_ context.Context, original, candidate string, desc ocispec.Descriptor) error {
	_, pinned, found := strings.Cut(original, "@")
	if !found || !strings.HasPrefix(pinned, "sha256:") {
		return fmt.Errorf("fallback %s refused: %s is not digest-pinned", candidate, original)
	}
	if desc.Digest.String() != pinned {
		return fmt.Errorf("fallback %s served %s, signed reference pins %s", candidate, desc.Digest, pinned)
	}
	return nil
}

// Localize is the render-time rewrite: it re-hosts ref onto the cluster-local
// registry without contacting it. With no local registry (online profile) the
// reference is returned unchanged.
func (r *Resolver) Localize(ref string) string {
	if r == nil || r.Local == "" || ref == "" {
		return ref
	}
	out, err := Rehost(ref, r.Local)
	if err != nil {
		return ref
	}
	return out
}

// Enabled reports whether a cluster-local registry is configured.
func (r *Resolver) Enabled() bool { return r != nil && r.Local != "" }

// Resolve consults layer 1 (cluster-local), then each opt-in fallback, then
// fails closed. Every outcome is logged with layer=<name>.
func (r *Resolver) Resolve(ctx context.Context, ref string) (Resolved, error) {
	if r == nil || r.Local == "" {
		r.log(ctx, slog.LevelInfo, ref, Resolved{Ref: ref, Layer: LayerOnline})
		return Resolved{Ref: ref, Layer: LayerOnline}, nil
	}
	local, err := Rehost(ref, r.Local)
	if err != nil {
		return Resolved{Layer: LayerFailClosed}, fmt.Errorf("%w: %w", ErrFailClosed, err)
	}
	misses := []string{}
	desc, err := r.head(ctx, local)
	if err == nil {
		out := Resolved{Ref: local, Layer: LayerLocal, Digest: desc.Digest.String()}
		r.log(ctx, slog.LevelInfo, ref, out)
		return out, nil
	}
	misses = append(misses, fmt.Sprintf("local %s: %v", local, err))
	for _, fallback := range r.Fallbacks {
		candidate, rehostErr := Rehost(ref, fallback)
		if rehostErr != nil {
			misses = append(misses, rehostErr.Error())
			continue
		}
		desc, err := r.head(ctx, candidate)
		if err != nil {
			misses = append(misses, fmt.Sprintf("fallback %s: %v", candidate, err))
			continue
		}
		if err := r.Verify(ctx, ref, candidate, desc); err != nil {
			misses = append(misses, err.Error())
			continue
		}
		out := Resolved{Ref: candidate, Layer: LayerFallback, Digest: desc.Digest.String()}
		r.log(ctx, slog.LevelWarn, ref, out)
		return out, nil
	}
	out := Resolved{Layer: LayerFailClosed}
	r.Logger.Log(ctx, slog.LevelError, "registry resolution", "layer", LayerFailClosed, "ref", ref, "misses", misses)
	return out, fmt.Errorf("%w for %s: %s", ErrFailClosed, ref, strings.Join(misses, "; "))
}

func (r *Resolver) log(ctx context.Context, level slog.Level, ref string, out Resolved) {
	logger := slog.Default()
	if r != nil && r.Logger != nil {
		logger = r.Logger
	}
	logger.Log(ctx, level, "registry resolution", "layer", out.Layer, "ref", ref, "resolved", out.Ref, "digest", out.Digest)
}

func (r *Resolver) head(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	parsed, err := orasregistry.ParseReference(strings.TrimPrefix(ref, "oci://"))
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("parse %s: %w", ref, err)
	}
	if parsed.Reference == "" {
		parsed.Reference = "latest"
	}
	repo, err := remote.NewRepository(parsed.Registry + "/" + parsed.Repository)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	repo.PlainHTTP = r.PlainHTTP[parsed.Registry]
	if r.Client != nil {
		repo.Client = r.Client
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return repo.Resolve(ctx, parsed.Reference)
}
