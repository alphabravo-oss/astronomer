package resolver

import (
	"bytes"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ArtifactConfigurationMatches binds separately retained configuration bytes to
// their original OCI descriptor. Inline configuration is supported only for the
// OCI empty object observed in the reviewed supplier manifests. It is not an
// alternate content location or permission to inline a payload layer. Callers
// must independently apply their configuration schema and signature policy.
func ArtifactConfigurationMatches(descriptor ocispec.Descriptor, config []byte) bool {
	if descriptor.Digest.Validate() != nil || descriptor.Digest.Algorithm() != digest.SHA256 || descriptor.Size <= 0 || descriptor.Size > 1<<20 || int64(len(config)) != descriptor.Size || digest.FromBytes(config) != descriptor.Digest || len(descriptor.URLs) != 0 {
		return false
	}
	if len(descriptor.Data) == 0 {
		return true
	}
	return descriptor.MediaType == "application/vnd.oci.empty.v1+json" && bytes.Equal(config, []byte("{}")) && bytes.Equal(descriptor.Data, config)
}
