package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const (
	defaultDeliveryPollInterval   = 30 * time.Second
	defaultDeliveryStatusInterval = 15 * time.Second
	deliveryResponseTimeout       = 30 * time.Second
)

type Sender func(*protocol.Message) error

type CapabilityProbe interface {
	Inspect(context.Context) (protocol.DeliveryControllerInventory, Capabilities, error)
}

type RuntimeConfig struct {
	ClusterID        string
	AgentVersion     string
	PollInterval     time.Duration
	StatusInterval   time.Duration
	ValidationPolicy ValidationPolicy
	Connected        func() bool
	Logger           *slog.Logger
}

type stateReply struct {
	requestID string
	response  protocol.DeliveryStateResponseV2
	errorCode string
}

// Runtime is the protocol-v2 lifecycle coordinator. It owns no generic YAML
// path: complete typed snapshots are validated and materialized before the
// Executor is allowed to write Kubernetes, while Flux remains the continuous
// reconciler during management-plane outages.
type Runtime struct {
	config   RuntimeConfig
	executor *Executor
	store    CheckpointStore
	probe    CapabilityProbe
	system   *SystemManager

	replies chan stateReply
	wake    chan struct{}
	paused  *atomic.Bool

	checkpoint checkpoint
	transient  map[string]protocol.DeliveryDeploymentStatusV2
	sequence   int64
}

// SetSystemManager installs the separate fixed-name reconciler for the signed
// Flux/agent distribution. It is intentionally not part of Executor, whose
// closed workload graph can never address the delivery system namespace.
func (r *Runtime) SetSystemManager(manager *SystemManager) {
	if r != nil {
		r.system = manager
	}
}

func NewRuntime(config RuntimeConfig, executor *Executor, store CheckpointStore, probe CapabilityProbe) (*Runtime, error) {
	parsed, err := uuid.Parse(config.ClusterID)
	if err != nil || parsed.String() != config.ClusterID {
		return nil, errors.New("delivery runtime cluster ID must be a canonical UUID")
	}
	if executor == nil || store == nil || probe == nil {
		return nil, errors.New("delivery runtime executor, checkpoint store, and capability probe are required")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = defaultDeliveryPollInterval
	}
	if config.StatusInterval <= 0 {
		config.StatusInterval = defaultDeliveryStatusInterval
	}
	if config.Connected == nil {
		config.Connected = func() bool { return true }
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Runtime{
		config: config, executor: executor, store: store, probe: probe,
		replies: make(chan stateReply, 4), wake: make(chan struct{}, 1),
		checkpoint: emptyCheckpoint(), transient: make(map[string]protocol.DeliveryDeploymentStatusV2),
	}, nil
}

func (r *Runtime) SetPauseGuard(guard *atomic.Bool) {
	if r != nil {
		r.paused = guard
	}
}

// HandleStateResponse is registered directly on the authenticated tunnel. It
// performs strict decoding but no Kubernetes I/O; the single Run goroutine
// serializes all snapshots and side effects.
func (r *Runtime) HandleStateResponse(_ context.Context, message *protocol.Message) (*protocol.Message, error) {
	reply := stateReply{requestID: message.RequestID, errorCode: strings.TrimSpace(message.Error)}
	if reply.errorCode == "" {
		decoder := json.NewDecoder(strings.NewReader(string(message.Payload)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&reply.response); err != nil {
			reply.errorCode = "invalid_delivery_state_response"
		} else if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			reply.errorCode = "invalid_delivery_state_response"
		}
	}
	select {
	case r.replies <- reply:
	default:
		r.config.Logger.Warn("delivery state response dropped because no request is waiting")
	}
	return nil, nil
}

// HandleReconcile treats server pushes only as wake-up hints. It never trusts
// push payload as desired state and always pulls the full authoritative snapshot.
func (r *Runtime) HandleReconcile(_ context.Context, _ *protocol.Message) (*protocol.Message, error) {
	select {
	case r.wake <- struct{}{}:
	default:
	}
	return nil, nil
}

func (r *Runtime) Run(ctx context.Context, send Sender) error {
	if send == nil {
		return errors.New("delivery runtime sender is required")
	}
	loaded, err := r.store.Load(ctx)
	if err != nil {
		return fmt.Errorf("load delivery checkpoint: %w", err)
	}
	r.checkpoint = loaded

	poll := time.NewTicker(r.config.PollInterval)
	status := time.NewTicker(r.config.StatusInterval)
	disconnected := time.NewTicker(time.Second)
	defer poll.Stop()
	defer status.Stop()
	defer disconnected.Stop()

	request := true
	for {
		if request && r.config.Connected() {
			if err := r.requestAndReconcile(ctx, send); err != nil && ctx.Err() == nil {
				r.config.Logger.Warn("delivery reconciliation did not complete", "error_code", stableRuntimeError(err))
			}
			request = false
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-poll.C:
			request = true
		case <-r.wake:
			request = true
		case <-disconnected.C:
			if r.config.Connected() {
				request = true
			}
		case <-status.C:
			if r.config.Connected() {
				if err := r.sendStatus(ctx, send); err != nil && ctx.Err() == nil {
					r.config.Logger.Warn("delivery status send failed", "error_code", stableRuntimeError(err))
				}
			}
		}
	}
}

func (r *Runtime) requestAndReconcile(ctx context.Context, send Sender) error {
	if r.paused != nil && r.paused.Load() {
		return nil
	}
	inventory, capabilities, err := r.probe.Inspect(ctx)
	if err != nil {
		return fmt.Errorf("inspect delivery capabilities: %w", err)
	}
	inventory.AgentVersion = strings.TrimSpace(r.config.AgentVersion)
	request := protocol.DeliveryStateRequestV2{
		ClusterID: r.config.ClusterID, ProtocolVersion: protocol.DeliveryProtocolVersion,
		AckedSnapshotGeneration: r.checkpoint.SnapshotGeneration, AckedETag: r.checkpoint.SnapshotETag,
		ControllerInventory: inventory,
	}
	if err := request.Validate(); err != nil {
		return fmt.Errorf("build delivery state request: %w", err)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode delivery state request: %w", err)
	}
	requestID := uuid.NewString()
	for {
		select {
		case <-r.replies:
			continue
		default:
		}
		break
	}
	if err := send(&protocol.Message{
		Type: protocol.MsgDeliveryStateRequest, ClusterID: r.config.ClusterID,
		StreamID: requestID, RequestID: requestID, Timestamp: time.Now().UTC(), Payload: payload,
	}); err != nil {
		return fmt.Errorf("send delivery state request: %w", err)
	}

	timer := time.NewTimer(deliveryResponseTimeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("delivery state response timed out")
		case reply := <-r.replies:
			if reply.requestID != "" && reply.requestID != requestID {
				continue
			}
			if reply.errorCode != "" {
				return fmt.Errorf("server rejected delivery state request: %s", sanitizeErrorCode(reply.errorCode))
			}
			if err := r.processSnapshot(ctx, reply.response, capabilities); err != nil {
				return err
			}
			return r.sendStatusWithInventory(ctx, send, inventory)
		}
	}
}

func (r *Runtime) processSnapshot(ctx context.Context, snapshot protocol.DeliveryStateResponseV2, capabilities Capabilities) error {
	defer zeroSnapshotCredentials(&snapshot)
	if err := ValidateSnapshot(snapshot, capabilities, r.config.ValidationPolicy); err != nil {
		return fmt.Errorf("validate delivery snapshot: %w", err)
	}
	if snapshot.SnapshotGeneration < r.checkpoint.SnapshotGeneration ||
		(snapshot.SnapshotGeneration == r.checkpoint.SnapshotGeneration && r.checkpoint.SnapshotETag != "" && snapshot.ETag != r.checkpoint.SnapshotETag) {
		return errors.New("delivery snapshot is stale or conflicts with the accepted generation")
	}
	if snapshot.NotModified {
		return nil
	}

	materializations := make(map[string]Materialization, len(snapshot.Assignments))
	for index := range snapshot.Assignments {
		assignment := snapshot.Assignments[index]
		materialization, err := BuildAssignment(assignment, capabilities, r.config.ValidationPolicy)
		if err != nil {
			return fmt.Errorf("precompute assignment %q: %w", assignment.DeploymentID, err)
		}
		materializations[assignment.DeploymentID] = materialization
	}
	if snapshot.System != nil {
		if r.system == nil {
			return errors.New("signed system release received without a configured system manager")
		}
		complete, err := r.system.Reconcile(ctx, *snapshot.System)
		if err != nil {
			return fmt.Errorf("reconcile signed system release: %w", err)
		}
		if !complete {
			return errors.New("signed system release remains in progress")
		}
	}

	complete := true
	desired := make(map[string]struct{}, len(snapshot.Assignments)+len(snapshot.Deletions))
	for index := range snapshot.Assignments {
		assignment := snapshot.Assignments[index]
		desired[assignment.DeploymentID] = struct{}{}
		if accepted, found := r.checkpoint.Assignments[assignment.DeploymentID]; found {
			if assignment.Generation < accepted.Generation || (assignment.Generation == accepted.Generation && assignment.SpecDigest != accepted.SpecDigest) {
				return fmt.Errorf("assignment %q regresses or changes an accepted generation", assignment.DeploymentID)
			}
		}
		materialization := materializations[assignment.DeploymentID]
		if err := r.executor.Apply(ctx, materialization); err != nil {
			complete = false
			r.transient[assignment.DeploymentID] = localFailureStatus(assignment, "local_apply_failed")
			continue
		}
		existing, err := r.executor.Existing(ctx, assignment, materialization)
		if err != nil {
			complete = false
			r.transient[assignment.DeploymentID] = localFailureStatus(assignment, "local_inventory_failed")
			continue
		}
		prune, err := PlanPrune(assignment, materialization, existing, true)
		if err != nil {
			complete = false
			r.transient[assignment.DeploymentID] = localFailureStatus(assignment, "local_prune_refused")
			continue
		}
		if err := r.executor.Prune(ctx, prune); err != nil {
			complete = false
			r.transient[assignment.DeploymentID] = localFailureStatus(assignment, "local_prune_failed")
			continue
		}
		r.checkpoint.Assignments[assignment.DeploymentID] = AcceptAssignment(assignment, materialization)
		delete(r.transient, assignment.DeploymentID)
	}

	for index := range snapshot.Deletions {
		tombstone := snapshot.Deletions[index]
		desired[tombstone.DeploymentID] = struct{}{}
		accepted, found := r.checkpoint.Assignments[tombstone.DeploymentID]
		if !found {
			r.transient[tombstone.DeploymentID] = tombstoneStatus(tombstone, "removed")
			continue
		}
		removed, err := r.executor.BeginDeletion(ctx, accepted.boundaryAssignment(), tombstone, accepted.materializationBoundary(), accepted.Objects)
		if err != nil {
			complete = false
			r.transient[tombstone.DeploymentID] = tombstoneStatus(tombstone, "failed")
			continue
		}
		if !removed {
			complete = false
			r.transient[tombstone.DeploymentID] = tombstoneStatus(tombstone, "deleting")
			continue
		}
		delete(r.checkpoint.Assignments, tombstone.DeploymentID)
		r.transient[tombstone.DeploymentID] = tombstoneStatus(tombstone, "removed")
	}
	for deploymentID := range r.transient {
		if _, stillDesired := desired[deploymentID]; !stillDesired {
			delete(r.transient, deploymentID)
		}
	}
	if complete {
		r.checkpoint.SnapshotGeneration = snapshot.SnapshotGeneration
		r.checkpoint.SnapshotETag = snapshot.ETag
		r.checkpoint.CredentialEpoch = snapshot.CredentialEpoch
	}
	if err := r.store.Save(ctx, r.checkpoint); err != nil {
		return fmt.Errorf("persist delivery checkpoint: %w", err)
	}
	if !complete {
		return errors.New("delivery snapshot remains in progress")
	}
	return nil
}

func (r *Runtime) sendStatus(ctx context.Context, send Sender) error {
	inventory, _, err := r.probe.Inspect(ctx)
	if err != nil {
		return fmt.Errorf("inspect delivery status inventory: %w", err)
	}
	inventory.AgentVersion = strings.TrimSpace(r.config.AgentVersion)
	return r.sendStatusWithInventory(ctx, send, inventory)
}

func (r *Runtime) sendStatusWithInventory(ctx context.Context, send Sender, inventory protocol.DeliveryControllerInventory) error {
	statuses := make([]protocol.DeliveryDeploymentStatusV2, 0, len(r.checkpoint.Assignments)+len(r.transient))
	for _, deploymentID := range sortedAssignmentIDs(r.checkpoint.Assignments) {
		accepted := r.checkpoint.Assignments[deploymentID]
		source, reconciler, err := r.observe(ctx, accepted)
		if err != nil {
			statuses = append(statuses, protocol.DeliveryDeploymentStatusV2{
				DeploymentID: accepted.DeploymentID, Generation: accepted.Generation, SpecDigest: accepted.SpecDigest,
				Phase: "unknown", ErrorCode: "local_observation_failed", Message: "Flux state could not be read",
				ObservedAt: time.Now().UTC(),
			})
			continue
		}
		normalized, err := NormalizeAcceptedObservation(AcceptedObservation{
			Assignment: accepted, Source: source, Reconciler: reconciler, ObservedAt: time.Now().UTC(),
		})
		if err != nil {
			statuses = append(statuses, protocol.DeliveryDeploymentStatusV2{
				DeploymentID: accepted.DeploymentID, Generation: accepted.Generation, SpecDigest: accepted.SpecDigest,
				Phase: "unknown", ErrorCode: "local_observation_refused", Message: "Flux state failed its ownership fence",
				ObservedAt: time.Now().UTC(),
			})
			continue
		}
		statuses = append(statuses, normalized)
	}
	for deploymentID, status := range r.transient {
		if _, accepted := r.checkpoint.Assignments[deploymentID]; accepted {
			for index := range statuses {
				if statuses[index].DeploymentID == deploymentID {
					statuses[index] = status
					break
				}
			}
			continue
		}
		statuses = append(statuses, status)
	}
	coalesced, err := CoalesceStatuses(statuses)
	if err != nil {
		return err
	}
	r.sequence++
	payload := protocol.DeliveryStatusV2{
		ProtocolVersion: protocol.DeliveryProtocolVersion, ClusterID: r.config.ClusterID,
		SessionSequence: r.sequence, SnapshotGeneration: r.checkpoint.SnapshotGeneration,
		SnapshotETag: r.checkpoint.SnapshotETag, Deployments: coalesced, ControllerInventory: inventory,
	}
	if err := payload.Validate(); err != nil {
		return fmt.Errorf("validate delivery status: %w", err)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode delivery status: %w", err)
	}
	return send(&protocol.Message{Type: protocol.MsgDeliveryStatus, ClusterID: r.config.ClusterID, Timestamp: time.Now().UTC(), Payload: body})
}

func (r *Runtime) observe(ctx context.Context, accepted AcceptedAssignment) (*unstructured.Unstructured, *unstructured.Unstructured, error) {
	var sourceIdentity, reconcilerIdentity *ObjectIdentity
	for index := range accepted.Objects {
		identity := accepted.Objects[index]
		switch identity.Kind {
		case "GitRepository", "OCIRepository", "HelmRepository":
			copy := identity
			sourceIdentity = &copy
		case "Kustomization", "HelmRelease":
			copy := identity
			reconcilerIdentity = &copy
		}
	}
	if sourceIdentity == nil || reconcilerIdentity == nil {
		return nil, nil, errors.New("accepted assignment lacks Flux object identities")
	}
	source, err := r.executor.Get(ctx, *sourceIdentity)
	if err != nil {
		return nil, nil, err
	}
	reconciler, err := r.executor.Get(ctx, *reconcilerIdentity)
	if err != nil {
		return nil, nil, err
	}
	return source, reconciler, nil
}

func localFailureStatus(assignment protocol.DeliveryAssignmentV2, code string) protocol.DeliveryDeploymentStatusV2 {
	return protocol.DeliveryDeploymentStatusV2{
		DeploymentID: assignment.DeploymentID, Generation: assignment.Generation, SpecDigest: assignment.SpecDigest,
		Phase: "failed", ErrorCode: code, Message: "The typed assignment could not be applied locally",
		ObservedAt: time.Now().UTC(),
	}
}

func tombstoneStatus(tombstone protocol.DeliveryDeletionV2, phase string) protocol.DeliveryDeploymentStatusV2 {
	status := protocol.DeliveryDeploymentStatusV2{
		DeploymentID: tombstone.DeploymentID, Generation: tombstone.Generation, SpecDigest: tombstone.SpecDigest,
		Phase: phase, ObservedAt: time.Now().UTC(),
	}
	if phase == "failed" {
		status.ErrorCode = "local_deletion_failed"
		status.Message = "The deletion fence or Kubernetes operation failed"
	}
	return status
}

func zeroSnapshotCredentials(snapshot *protocol.DeliveryStateResponseV2) {
	if snapshot == nil {
		return
	}
	if snapshot.System != nil && snapshot.System.Credential != nil {
		for key, value := range snapshot.System.Credential.Data {
			for byteIndex := range value {
				value[byteIndex] = 0
			}
			delete(snapshot.System.Credential.Data, key)
		}
		snapshot.System.Credential = nil
	}
	for index := range snapshot.Assignments {
		credential := snapshot.Assignments[index].Credential
		if credential == nil {
			continue
		}
		for key, value := range credential.Data {
			for byteIndex := range value {
				value[byteIndex] = 0
			}
			delete(credential.Data, key)
		}
		snapshot.Assignments[index].Credential = nil
	}
}

func sanitizeErrorCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) > 96 {
		value = value[:96]
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return "delivery_state_unavailable"
		}
	}
	if value == "" {
		return "delivery_state_unavailable"
	}
	return value
}

func stableRuntimeError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	for _, entry := range []struct{ contains, code string }{
		{"timed out", "response_timeout"},
		{"capabilit", "capability_probe_failed"},
		{"validate", "snapshot_validation_failed"},
		{"stale", "stale_snapshot"},
		{"checkpoint", "checkpoint_failed"},
		{"in progress", "snapshot_in_progress"},
		{"server rejected", "server_rejected"},
	} {
		if strings.Contains(message, entry.contains) {
			return entry.code
		}
	}
	return "delivery_reconcile_failed"
}
