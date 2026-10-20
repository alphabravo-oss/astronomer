package airgap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/errdef"
	orasregistry "oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/alphabravocompany/astronomer-go/internal/registry"
)

// Mirror statuses.
const (
	StatusCopied  = "copied"
	StatusPresent = "present"
	StatusFailed  = "failed"
)

// cosignSuffixes are the cosign tag-scheme companions of a digest
// (signature, attestations, attached SBOM).
var cosignSuffixes = []string{".sig", ".att", ".sbom"}

// MirrorOptions configures a mirror run.
type MirrorOptions struct {
	// Destination is the cluster-local registry base (host[:port][/prefix]).
	Destination string
	// PlainHTTP lists registry hosts reached over plain HTTP.
	PlainHTTP map[string]bool
	// Client authenticates both sides (defaults to registry.DefaultClient:
	// the ambient docker credential store).
	Client remote.Client
	// Out receives one line per item.
	Out io.Writer
	// Concurrency bounds blob copies per item (oras default when zero).
	Concurrency int
}

// Result is the outcome of mirroring one plan item.
type Result struct {
	Item   Item
	Digest string
	Status string
	// Companions counts signatures/attestations/referrers copied or present.
	Companions int
	Err        error
}

// Mirror copies every plan item to opts.Destination with oras-go: the full
// manifest graph (multi-arch indexes and every platform manifest) is copied
// by digest, the source tag is preserved, cosign signature/attestation tags
// and OCI referrers of the root are copied when present, and the destination
// digest is verified. Items already present with the expected digest are not
// re-copied, and oras skips blobs the destination already holds, so a rerun
// after an interruption resumes where it stopped.
func Mirror(ctx context.Context, plan Plan, opts MirrorOptions) ([]Result, error) {
	if err := registry.ValidateBase(opts.Destination); err != nil {
		return nil, fmt.Errorf("--to: %w", err)
	}
	if opts.Client == nil {
		opts.Client = registry.DefaultClient()
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	results := make([]Result, 0, len(plan.Items))
	failed := 0
	for _, item := range plan.Items {
		result := mirrorItem(ctx, item, opts)
		if result.Err != nil {
			failed++
			_, _ = fmt.Fprintf(opts.Out, "%-7s %s -> %s: %v\n", result.Status, item.Source, result.Item.Target, result.Err)
		} else {
			_, _ = fmt.Fprintf(opts.Out, "%-7s %s -> %s %s (+%d signatures/attestations)\n", result.Status, item.Source, result.Item.Target, result.Digest, result.Companions)
		}
		results = append(results, result)
	}
	if failed > 0 {
		return results, fmt.Errorf("%d of %d items failed to mirror", failed, len(plan.Items))
	}
	return results, nil
}

type location struct {
	repo   *remote.Repository
	tag    string
	digest string
}

// ref returns the reference used to read: the pinned digest when known.
func (l location) ref() string {
	if l.digest != "" {
		return l.digest
	}
	return l.tag
}

func (opts MirrorOptions) locate(ref string) (location, error) {
	ref = strings.TrimPrefix(ref, "oci://")
	name, digest, _ := strings.Cut(ref, "@")
	tag := ""
	if index := strings.LastIndex(name, ":"); index > strings.LastIndex(name, "/") {
		name, tag = name[:index], name[index+1:]
	}
	host, path, err := registry.SplitHost(name)
	if err != nil {
		return location{}, err
	}
	if host == "docker.io" {
		// Docker Hub serves its API from registry-1.docker.io; short names live
		// under library/.
		host = "registry-1.docker.io"
		if !strings.Contains(path, "/") {
			path = "library/" + path
		}
	}
	if _, err := orasregistry.ParseReference(host + "/" + path); err != nil {
		return location{}, err
	}
	repo, err := remote.NewRepository(host + "/" + path)
	if err != nil {
		return location{}, err
	}
	repo.PlainHTTP = opts.PlainHTTP[host]
	repo.Client = opts.Client
	if tag == "" && digest == "" {
		tag = "latest"
	}
	return location{repo: repo, tag: tag, digest: digest}, nil
}

func mirrorItem(ctx context.Context, item Item, opts MirrorOptions) Result {
	result := Result{Item: item, Status: StatusFailed}
	target, err := registry.Rehost(item.Source, opts.Destination)
	if err != nil {
		result.Err = err
		return result
	}
	result.Item.Target = target
	src, err := opts.locate(item.Source)
	if err != nil {
		result.Err = err
		return result
	}
	dst, err := opts.locate(target)
	if err != nil {
		result.Err = err
		return result
	}
	root, err := src.repo.Resolve(ctx, src.ref())
	if err != nil {
		result.Err = fmt.Errorf("resolve source: %w", err)
		return result
	}
	if src.digest != "" && root.Digest.String() != src.digest {
		result.Err = fmt.Errorf("source served %s, reference pins %s", root.Digest, src.digest)
		return result
	}
	result.Digest = root.Digest.String()
	existing, err := dst.repo.Resolve(ctx, dst.ref())
	switch {
	case err == nil && existing.Digest == root.Digest:
		result.Status = StatusPresent
	case err != nil && !errors.Is(err, errdef.ErrNotFound):
		result.Err = fmt.Errorf("inspect destination: %w", err)
		return result
	default:
		copyOpts := oras.DefaultCopyOptions
		if opts.Concurrency > 0 {
			copyOpts.Concurrency = opts.Concurrency
		}
		if err := oras.CopyGraph(ctx, src.repo, dst.repo, root, copyOpts.CopyGraphOptions); err != nil {
			result.Err = fmt.Errorf("copy: %w", err)
			return result
		}
		result.Status = StatusCopied
	}
	if dst.tag != "" {
		if err := dst.repo.Tag(ctx, root, dst.tag); err != nil {
			result.Err = fmt.Errorf("tag %s: %w", dst.tag, err)
			result.Status = StatusFailed
			return result
		}
	}
	copied, err := dst.repo.Resolve(ctx, root.Digest.String())
	if err != nil || copied.Digest != root.Digest {
		result.Err = fmt.Errorf("destination digest verification failed: %v", err)
		result.Status = StatusFailed
		return result
	}
	companions, err := copyCompanions(ctx, src, dst, root)
	result.Companions = companions
	if err != nil {
		result.Err = err
		result.Status = StatusFailed
	}
	return result
}

// copyCompanions copies cosign tag-scheme companions and OCI referrers of root.
func copyCompanions(ctx context.Context, src, dst location, root ocispec.Descriptor) (int, error) {
	count := 0
	prefix := strings.Replace(root.Digest.String(), ":", "-", 1)
	for _, suffix := range cosignSuffixes {
		tag := prefix + suffix
		desc, err := src.repo.Resolve(ctx, tag)
		if errors.Is(err, errdef.ErrNotFound) {
			continue
		}
		if err != nil {
			return count, fmt.Errorf("resolve %s: %w", tag, err)
		}
		if err := oras.CopyGraph(ctx, src.repo, dst.repo, desc, oras.DefaultCopyGraphOptions); err != nil {
			return count, fmt.Errorf("copy %s: %w", tag, err)
		}
		if err := dst.repo.Tag(ctx, desc, tag); err != nil {
			return count, fmt.Errorf("tag %s: %w", tag, err)
		}
		count++
	}
	var referrers []ocispec.Descriptor
	err := src.repo.Referrers(ctx, root, "", func(page []ocispec.Descriptor) error {
		referrers = append(referrers, page...)
		return nil
	})
	if err != nil && !errors.Is(err, errdef.ErrNotFound) && !errors.Is(err, errdef.ErrUnsupported) {
		return count, fmt.Errorf("list referrers: %w", err)
	}
	for _, referrer := range referrers {
		if err := oras.CopyGraph(ctx, src.repo, dst.repo, referrer, oras.DefaultCopyGraphOptions); err != nil {
			return count, fmt.Errorf("copy referrer %s: %w", referrer.Digest, err)
		}
		count++
	}
	return count, nil
}
