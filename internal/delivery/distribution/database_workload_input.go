package distribution

import (
	"context"
	"errors"
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

// ReadWorkloadImageMetadata is a content-only read beneath the SAME pinned
// registry roots, mounted pull policy and authenticated measured-scan profile.
// No HTTP client or credential escapes; current workload/relational authority
// remains the owned worker's responsibility. A workload is not a vendor artifact.
func (authority *DatabaseAuthority) ReadWorkloadImageMetadata(ctx context.Context, proof *VerifiedDatabaseAuthorityBase, repository, subject, architecture string) (resolver.WorkloadImageMetadata, error) {
	if authority == nil || authority.registry == nil {
		return resolver.WorkloadImageMetadata{}, errors.New("owned workload requires pinned registry authority")
	}
	if _, err := authority.DatabaseScanImages(ctx, proof); err != nil {
		return resolver.WorkloadImageMetadata{}, err
	}
	metadata, err := resolver.FetchWorkloadImageMetadata(ctx, authority.registry.Client(), repository, subject, architecture)
	if err != nil {
		return resolver.WorkloadImageMetadata{}, err
	}
	if err := authority.Revalidate(ctx, proof); err != nil {
		return resolver.WorkloadImageMetadata{}, err
	}
	return metadata, nil
}

// CopyWorkloadImageLayer verifies complete bytes within finite range limits;
// this cannot certify scanner use or select a mutable tag. Per-request physical
// mounted credential/TLS revalidation is retained by the canonical transport.
func (authority *DatabaseAuthority) CopyWorkloadImageLayer(ctx context.Context, proof *VerifiedDatabaseAuthorityBase, repository string, layer ocispec.Descriptor, writer io.Writer) error {
	if authority == nil || authority.registry == nil {
		return errors.New("owned workload requires pinned registry authority")
	}
	if _, err := authority.DatabaseScanImages(ctx, proof); err != nil {
		return err
	}
	if err := resolver.StreamWorkloadImageLayer(ctx, authority.registry.Client(), repository, layer, writer); err != nil {
		return err
	}
	return authority.Revalidate(ctx, proof)
}
