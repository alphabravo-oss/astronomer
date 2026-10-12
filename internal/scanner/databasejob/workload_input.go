package databasejob

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	digest "github.com/opencontainers/go-digest"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

// WorkloadInputSnapshot is measured content DATA. Neither this snapshot nor a
// helper ACK proves the current target, Pod identity, scanner use or acceptance.
// Private OCI config bytes are deliberately excluded from this comparison data.
type WorkloadInputSnapshot struct {
	ManifestDigest string                `json:"manifest_digest"`
	Architecture   string                `json:"architecture"`
	IndexDigest    string                `json:"index_digest"`
	Members        []WorkloadInputMember `json:"members"`
}
type WorkloadInputMember struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

const maximumWorkloadInputBytes int64 = 8 << 30

type WorkloadInput struct {
	mutex                             sync.Mutex
	directory                         string
	directoryFD, blobsFD, algorithmFD *os.File
	files                             []*os.File
	snapshot                          WorkloadInputSnapshot
	closed                            bool
}

// CaptureWorkloadInput captures ONLY the exact platform manifest selected by
// the running owner. It does not resolve a tag or index, grant repository access
// or consume signing/push credentials. The actual worker must obtain the child
// digest from current Kubernetes/runtime identity and lock it before witness use.
func CaptureWorkloadInput(ctx context.Context, authority *distribution.DatabaseAuthority, proof *distribution.VerifiedDatabaseAuthorityBase, repository, subject, architecture, root string, claim uuid.UUID) (*WorkloadInput, error) {
	if ctx == nil || authority == nil || proof == nil || claim.Version() != 4 || claim.Variant() != uuid.RFC4122 || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, errors.New("workload input capture arguments invalid")
	}
	bounded, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	metadata, err := authority.ReadWorkloadImageMetadata(bounded, proof, repository, subject, architecture)
	if err != nil {
		return nil, err
	}
	owner, err := createWorkloadInput(bounded, root, claim, metadata, subject, architecture)
	if err != nil {
		return nil, err
	}
	accepted := false
	defer func() {
		if !accepted {
			owner.Close()
		}
	}()
	for _, layer := range metadata.Layers {
		descriptor := WorkloadInputMember{Name: "blobs/sha256/" + layer.Digest.Encoded(), Digest: layer.Digest.String(), Size: layer.Size}
		writer, existing, err := owner.createBlob(descriptor)
		if err != nil {
			return nil, err
		}
		if existing {
			if err := owner.measureExisting(bounded, descriptor); err != nil {
				return nil, err
			}
			continue
		} // Exact original digest/size is remeasured at sealing.
		if err := authority.CopyWorkloadImageLayer(bounded, proof, repository, layer, writer); err != nil {
			return nil, err
		}
	}
	if err := owner.sealAndMeasure(bounded); err != nil {
		return nil, err
	}
	accepted = true
	return owner, nil
}

// ReopenWorkloadInput performs whole-byte measurement against the preparation
// DATA, using no network or credential. It cannot grant provenance by itself.
func ReopenWorkloadInput(ctx context.Context, root string, claim uuid.UUID, expected WorkloadInputSnapshot) (*WorkloadInput, error) {
	if ctx == nil || claim.Version() != 4 || claim.Variant() != uuid.RFC4122 || !filepath.IsAbs(root) || filepath.Clean(root) != root || validateWorkloadInputSnapshot(expected) != nil {
		return nil, errors.New("workload input reopen arguments invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return reopenWorkloadInput(ctx, root, claim, expected)
}
func (owner *WorkloadInput) Measure(ctx context.Context) (WorkloadInputSnapshot, error) {
	if owner == nil || ctx == nil {
		return WorkloadInputSnapshot{}, errors.New("workload input measurement unavailable")
	}
	if err := ctx.Err(); err != nil {
		return WorkloadInputSnapshot{}, err
	}
	owner.mutex.Lock()
	defer owner.mutex.Unlock()
	if owner.closed {
		return WorkloadInputSnapshot{}, errors.New("workload input closed")
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := owner.measure(bounded); err != nil {
		return WorkloadInputSnapshot{}, err
	}
	value := owner.snapshot
	value.Members = append([]WorkloadInputMember(nil), value.Members...)
	return value, nil
}
func (owner *WorkloadInput) Close() {
	if owner == nil {
		return
	}
	owner.mutex.Lock()
	defer owner.mutex.Unlock()
	if owner.closed {
		return
	}
	owner.closed = true
	for _, file := range owner.files {
		if file != nil {
			_ = file.Close()
		}
	}
	for _, file := range []*os.File{owner.algorithmFD, owner.blobsFD, owner.directoryFD} {
		if file != nil {
			_ = file.Close()
		}
	}
	// Partial inputs are retained; this owner never deletes/replaces an output.
}
func validateWorkloadInputSnapshot(value WorkloadInputSnapshot) error {
	if !recordDigest.MatchString(value.ManifestDigest) || !recordDigest.MatchString(value.IndexDigest) || (value.Architecture != "amd64" && value.Architecture != "arm64") || len(value.Members) < 4 || len(value.Members) > 36 {
		return errors.New("workload input comparison identity invalid")
	}
	seen := map[string]bool{}
	var total int64
	for _, member := range value.Members {
		selected := digest.Digest(member.Digest)
		if selected.Validate() != nil || selected.Algorithm() != digest.SHA256 || seen[member.Name] || member.Size <= 0 || member.Size > maximumWorkloadInputBytes-total {
			return errors.New("workload input comparison member invalid")
		}
		switch member.Name {
		case "oci-layout":
			if member.Size > 128 {
				return errors.New("workload layout metadata bound invalid")
			}
		case "index.json":
			if member.Size > 4<<10 || member.Digest != value.IndexDigest {
				return errors.New("workload index comparison invalid")
			}
		default:
			if member.Name == "blobs/sha256/"+digest.Digest(value.ManifestDigest).Encoded() && member.Size > 1<<20 {
				return errors.New("workload manifest byte bound invalid")
			}
			if member.Name != "blobs/sha256/"+selected.Encoded() || member.Size > 2<<30 {
				return errors.New("workload blob literal identity invalid")
			}
		}
		total += member.Size
		seen[member.Name] = true
	}
	if !seen["oci-layout"] || !seen["index.json"] || !seen["blobs/sha256/"+digest.Digest(value.ManifestDigest).Encoded()] {
		return errors.New("workload comparison missing literal closure")
	}
	return nil
}
