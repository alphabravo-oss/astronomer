package distribution

import (
	"errors"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	EmbeddedAssetContract           = 1
	DescriptorAssetContract         = 2
	MaximumDatabaseLayerBytes int64 = 2 << 30
	MaximumDatabaseKitBytes   int64 = 8 << 30
)

// DatabaseDescriptor is an authenticated content claim, not proof that a
// payload was downloaded/hashed or that its vulnerability metadata is fresh.
// Only the two fixed database roles may use the separate streamed child bound.
type DatabaseDescriptor struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type"`
}

func (descriptor *DatabaseDescriptor) validate(role string) error {
	media := "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip"
	if role == "trivy-java-database" {
		media = "application/vnd.aquasec.trivy.javadb.layer.v1.tar+gzip"
	} else if role != "trivy-database" {
		return errors.New("database descriptor assigned to unrelated role")
	}
	if descriptor == nil || !digestPattern.MatchString(descriptor.Digest) || descriptor.Size <= 0 || descriptor.Size > MaximumDatabaseLayerBytes || descriptor.MediaType != media {
		return errors.New("database descriptor claim invalid")
	}
	return nil
}
func (descriptor DatabaseDescriptor) matches(layer ocispec.Descriptor) bool {
	return descriptor.Digest == layer.Digest.String() && descriptor.Size == layer.Size && descriptor.MediaType == layer.MediaType && len(layer.URLs) == 0 && len(layer.Data) == 0
}
