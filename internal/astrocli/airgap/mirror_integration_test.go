//go:build integration

package airgap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"

	"github.com/alphabravocompany/astronomer-go/internal/testzot"
)

func TestMirrorBetweenRegistries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	source := testzot.Start(t)
	destination := testzot.Start(t)

	index := testzot.PushIndex(t, testzot.Repository(t, source+"/alphabravo-oss/astronomer-go-server"), "v9.9.9", "server")
	signature := testzot.PushReferrer(t, testzot.Repository(t, source+"/alphabravo-oss/astronomer-go-server"), index, "server")
	// cosign tag-scheme signature next to the image.
	sigTag := strings.Replace(index.Digest.String(), ":", "-", 1) + ".sig"
	sigDesc := testzot.PushImage(t, testzot.Repository(t, source+"/alphabravo-oss/astronomer-go-server"), sigTag, "cosign-sig")
	flux := testzot.PushImage(t, testzot.Repository(t, source+"/fluxcd/helm-controller"), "unused", "flux")

	plan := Plan{SchemaVersion: 1, Items: []Item{
		{Kind: KindImage, Source: source + "/alphabravo-oss/astronomer-go-server:v9.9.9"},
		{Kind: KindImage, Source: source + "/fluxcd/helm-controller@" + flux.Digest.String()},
	}}
	opts := MirrorOptions{Destination: destination, PlainHTTP: map[string]bool{source: true, destination: true}}
	var out bytes.Buffer
	opts.Out = &out

	results, err := Mirror(ctx, plan, opts)
	if err != nil {
		t.Fatalf("mirror: %v\n%s", err, out.String())
	}
	for _, result := range results {
		if result.Status != StatusCopied {
			t.Fatalf("first run %s status %s", result.Item.Source, result.Status)
		}
	}
	if results[0].Companions != 2 {
		t.Fatalf("expected the cosign tag and the referrer to be copied, got %d", results[0].Companions)
	}

	dst := testzot.Repository(t, destination+"/alphabravo-oss/astronomer-go-server")
	got, err := dst.Resolve(ctx, "v9.9.9")
	if err != nil || got.Digest != index.Digest {
		t.Fatalf("destination tag digest = %v (%v), want %s", got.Digest, err, index.Digest)
	}
	rc, err := dst.Fetch(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := content.ReadAll(rc, got)
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	var copiedIndex ocispec.Index
	if err := json.Unmarshal(raw, &copiedIndex); err != nil || len(copiedIndex.Manifests) != 2 {
		t.Fatalf("multi-arch index not preserved: %v %d", err, len(copiedIndex.Manifests))
	}
	for _, platform := range copiedIndex.Manifests {
		if _, err := dst.Resolve(ctx, platform.Digest.String()); err != nil {
			t.Fatalf("platform manifest %s missing: %v", platform.Platform.Architecture, err)
		}
	}
	if desc, err := dst.Resolve(ctx, sigTag); err != nil || desc.Digest != sigDesc.Digest {
		t.Fatalf("cosign signature tag not mirrored: %v", err)
	}
	var referrers []ocispec.Descriptor
	if err := dst.Referrers(ctx, index, "", func(page []ocispec.Descriptor) error { referrers = append(referrers, page...); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(referrers) != 1 || referrers[0].Digest != signature.Digest {
		t.Fatalf("referrer not mirrored: %v", referrers)
	}
	if _, err := testzot.Repository(t, destination+"/fluxcd/helm-controller").Resolve(ctx, flux.Digest.String()); err != nil {
		t.Fatalf("digest-only item missing: %v", err)
	}

	// Idempotent rerun: nothing is copied again.
	out.Reset()
	results, err = Mirror(ctx, plan, opts)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	for _, result := range results {
		if result.Status != StatusPresent {
			t.Fatalf("rerun %s status %s, want present", result.Item.Source, result.Status)
		}
	}

	// A pinned digest the source does not serve fails, and the run reports it.
	bad := Plan{SchemaVersion: 1, Items: []Item{{Kind: KindImage, Source: source + "/fluxcd/helm-controller@sha256:" + strings.Repeat("0", 64)}}}
	opts.Out = io.Discard
	if _, err := Mirror(ctx, bad, opts); err == nil {
		t.Fatal("missing source digest must fail")
	}
}
