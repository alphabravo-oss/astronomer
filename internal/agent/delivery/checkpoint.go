package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

const (
	checkpointSchemaVersion = 1
	checkpointName          = "astronomer-delivery-checkpoint"
	checkpointDataKey       = "checkpoint.json"
	checkpointShardCount    = 64
	checkpointMaxBytes      = 900 << 10
	checkpointLabel         = "delivery.astronomer.io/checkpoint"
)

type checkpoint struct {
	SchemaVersion      int                           `json:"schema_version"`
	SnapshotGeneration int64                         `json:"snapshot_generation"`
	SnapshotETag       string                        `json:"snapshot_etag,omitempty"`
	CredentialEpoch    int64                         `json:"credential_epoch"`
	Assignments        map[string]AcceptedAssignment `json:"assignments"`
}

type checkpointSummary struct {
	SchemaVersion      int    `json:"schema_version"`
	SnapshotGeneration int64  `json:"snapshot_generation"`
	SnapshotETag       string `json:"snapshot_etag,omitempty"`
	CredentialEpoch    int64  `json:"credential_epoch"`
}

type checkpointShard struct {
	SchemaVersion int                           `json:"schema_version"`
	Assignments   map[string]AcceptedAssignment `json:"assignments"`
}

type CheckpointStore interface {
	Load(context.Context) (checkpoint, error)
	Save(context.Context, checkpoint) error
}

// KubernetesCheckpointStore shards credential-free accepted identities so the
// protocol's 10,000-assignment bound can never overrun Kubernetes' ConfigMap
// object limit. The summary is committed last; a crash during shard updates
// therefore causes a safe full snapshot replay rather than a false ack.
type KubernetesCheckpointStore struct {
	client    kubernetes.Interface
	namespace string
}

func NewKubernetesCheckpointStore(client kubernetes.Interface, namespace string) (*KubernetesCheckpointStore, error) {
	if client == nil {
		return nil, errors.New("checkpoint Kubernetes client is required")
	}
	if strings.TrimSpace(namespace) == "" {
		return nil, errors.New("checkpoint namespace is required")
	}
	return &KubernetesCheckpointStore{client: client, namespace: namespace}, nil
}

func (s *KubernetesCheckpointStore) Load(ctx context.Context) (checkpoint, error) {
	result := emptyCheckpoint()
	list, err := s.client.CoreV1().ConfigMaps(s.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: checkpointLabel + "=true",
	})
	if err != nil {
		return result, fmt.Errorf("list delivery checkpoints: %w", err)
	}
	foundSummary := false
	for index := range list.Items {
		object := &list.Items[index]
		if object.Name == checkpointName {
			var summary checkpointSummary
			if err := strictJSON([]byte(object.Data[checkpointDataKey]), &summary); err != nil {
				return result, fmt.Errorf("decode delivery checkpoint summary: %w", err)
			}
			if summary.SchemaVersion != checkpointSchemaVersion {
				return result, fmt.Errorf("unsupported delivery checkpoint schema %d", summary.SchemaVersion)
			}
			result.SnapshotGeneration = summary.SnapshotGeneration
			result.SnapshotETag = summary.SnapshotETag
			result.CredentialEpoch = summary.CredentialEpoch
			foundSummary = true
			continue
		}
		if !strings.HasPrefix(object.Name, checkpointName+"-") {
			continue
		}
		var shard checkpointShard
		if err := strictJSON([]byte(object.Data[checkpointDataKey]), &shard); err != nil {
			return result, fmt.Errorf("decode delivery checkpoint shard %q: %w", object.Name, err)
		}
		if shard.SchemaVersion != checkpointSchemaVersion {
			return result, fmt.Errorf("unsupported delivery checkpoint shard schema %d", shard.SchemaVersion)
		}
		for deploymentID, assignment := range shard.Assignments {
			if deploymentID != assignment.DeploymentID {
				return result, fmt.Errorf("checkpoint assignment key does not match deployment %q", deploymentID)
			}
			if _, duplicate := result.Assignments[deploymentID]; duplicate {
				return result, fmt.Errorf("duplicate checkpoint assignment %q", deploymentID)
			}
			result.Assignments[deploymentID] = assignment
		}
	}
	if !foundSummary && len(result.Assignments) != 0 {
		// Shards without a committed summary may be remnants of an interrupted
		// first save. Keep their accepted object fences, but advertise no ack so
		// the server resends the authoritative full snapshot.
		result.SnapshotGeneration = 0
		result.SnapshotETag = ""
		result.CredentialEpoch = 0
	}
	if err := validateCheckpoint(result); err != nil {
		return emptyCheckpoint(), err
	}
	return result, nil
}

func (s *KubernetesCheckpointStore) Save(ctx context.Context, value checkpoint) error {
	if err := validateCheckpoint(value); err != nil {
		return err
	}
	shards := make([]map[string]AcceptedAssignment, checkpointShardCount)
	for index := range shards {
		shards[index] = make(map[string]AcceptedAssignment)
	}
	for deploymentID, assignment := range value.Assignments {
		digest := sha256.Sum256([]byte(deploymentID))
		index := int(digest[0]) % checkpointShardCount
		shards[index][deploymentID] = assignment
	}

	expected := make(map[string][]byte)
	for index, assignments := range shards {
		if len(assignments) == 0 {
			continue
		}
		payload, err := json.Marshal(checkpointShard{SchemaVersion: checkpointSchemaVersion, Assignments: assignments})
		if err != nil {
			return fmt.Errorf("encode checkpoint shard %d: %w", index, err)
		}
		if len(payload) > checkpointMaxBytes {
			return fmt.Errorf("checkpoint shard %d exceeds %d bytes", index, checkpointMaxBytes)
		}
		expected[checkpointShardName(index)] = payload
	}

	current, err := s.client.CoreV1().ConfigMaps(s.namespace).List(ctx, metav1.ListOptions{LabelSelector: checkpointLabel + "=true"})
	if err != nil {
		return fmt.Errorf("list existing delivery checkpoints: %w", err)
	}
	for name, payload := range expected {
		if err := s.upsert(ctx, name, payload); err != nil {
			return err
		}
	}
	for index := range current.Items {
		name := current.Items[index].Name
		if name == checkpointName || !strings.HasPrefix(name, checkpointName+"-") {
			continue
		}
		if _, keep := expected[name]; keep {
			continue
		}
		if err := s.client.CoreV1().ConfigMaps(s.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete stale delivery checkpoint shard %q: %w", name, err)
		}
	}

	summary, err := json.Marshal(checkpointSummary{
		SchemaVersion: checkpointSchemaVersion, SnapshotGeneration: value.SnapshotGeneration,
		SnapshotETag: value.SnapshotETag, CredentialEpoch: value.CredentialEpoch,
	})
	if err != nil {
		return fmt.Errorf("encode checkpoint summary: %w", err)
	}
	return s.upsert(ctx, checkpointName, summary)
}

func (s *KubernetesCheckpointStore) upsert(ctx context.Context, name string, payload []byte) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		configMaps := s.client.CoreV1().ConfigMaps(s.namespace)
		current, err := configMaps.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, err = configMaps.Create(ctx, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.namespace, Labels: map[string]string{checkpointLabel: "true"}},
				Data:       map[string]string{checkpointDataKey: string(payload)},
			}, metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}
		if current.Labels[checkpointLabel] != "true" {
			return fmt.Errorf("refusing to overwrite unowned ConfigMap %s/%s", s.namespace, name)
		}
		if current.Data[checkpointDataKey] == string(payload) {
			return nil
		}
		next := current.DeepCopy()
		if next.Data == nil {
			next.Data = make(map[string]string)
		}
		next.Data[checkpointDataKey] = string(payload)
		_, err = configMaps.Update(ctx, next, metav1.UpdateOptions{})
		return err
	})
}

func emptyCheckpoint() checkpoint {
	return checkpoint{SchemaVersion: checkpointSchemaVersion, Assignments: make(map[string]AcceptedAssignment)}
}

func validateCheckpoint(value checkpoint) error {
	if value.SchemaVersion != checkpointSchemaVersion {
		return fmt.Errorf("unsupported delivery checkpoint schema %d", value.SchemaVersion)
	}
	if value.SnapshotGeneration < 0 || value.CredentialEpoch < 0 ||
		(value.SnapshotGeneration == 0 && value.SnapshotETag != "") ||
		(value.SnapshotGeneration > 0 && !digestValuePattern.MatchString(value.SnapshotETag)) {
		return errors.New("delivery checkpoint has invalid snapshot metadata")
	}
	if len(value.Assignments) > 10_000 {
		return errors.New("delivery checkpoint exceeds assignment limit")
	}
	for deploymentID, assignment := range value.Assignments {
		if deploymentID != assignment.DeploymentID {
			return fmt.Errorf("checkpoint key does not match assignment %q", deploymentID)
		}
		if err := validateAcceptedAssignment(assignment); err != nil {
			return fmt.Errorf("checkpoint assignment %q: %w", deploymentID, err)
		}
		if len(assignment.Objects) == 0 || len(assignment.Objects) > 16 {
			return fmt.Errorf("checkpoint assignment %q has an invalid object count", deploymentID)
		}
		seen := make(map[ObjectIdentity]struct{}, len(assignment.Objects))
		for _, identity := range assignment.Objects {
			if identity.Name == "" {
				return fmt.Errorf("checkpoint assignment %q has an unnamed object", deploymentID)
			}
			if _, duplicate := seen[identity]; duplicate {
				return fmt.Errorf("checkpoint assignment %q has duplicate object %s", deploymentID, identity)
			}
			seen[identity] = struct{}{}
			if identity.Kind == "Namespace" {
				if identity.Name != assignment.ControlNamespace || identity.Namespace != "" {
					return fmt.Errorf("checkpoint assignment %q has an invalid Namespace fence", deploymentID)
				}
				continue
			}
			if err := validatePruneIdentity(assignment.boundaryAssignment(), assignment.materializationBoundary(), identity); err != nil {
				return fmt.Errorf("checkpoint assignment %q: %w", deploymentID, err)
			}
		}
	}
	return nil
}

func checkpointShardName(index int) string {
	return checkpointName + "-" + fmt.Sprintf("%02d", index)
}

func strictJSON(payload []byte, target any) error {
	if len(payload) == 0 {
		return errors.New("checkpoint payload is empty")
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("checkpoint contains trailing JSON")
	}
	return nil
}

func sortedAssignmentIDs(assignments map[string]AcceptedAssignment) []string {
	ids := make([]string, 0, len(assignments))
	for id := range assignments {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
