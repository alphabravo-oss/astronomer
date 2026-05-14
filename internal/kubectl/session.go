// Package kubectl — session lifecycle.
package kubectl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// K8sRequester is the surface the lifecycle uses to talk to the
// in-cluster apiserver via the tunnel. Same shape as
// tasks.ProjectK8sRequester so the production wiring can pass the same
// adapter. Tests pass a recording fake.
type K8sRequester interface {
	Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*K8sResponse, error)
}

// K8sResponse mirrors tunnel.K8sResponsePayload — minimal subset the
// session code needs. Body is the raw JSON the apiserver returned.
type K8sResponse struct {
	StatusCode int
	Body       []byte
}

// SessionQuerier is the slice of sqlc.Queries the session package needs.
// Tests pass a fake; production wires *sqlc.Queries.
type SessionQuerier interface {
	CreateKubectlSession(ctx context.Context, arg sqlc.CreateKubectlSessionParams) (sqlc.KubectlSession, error)
	GetKubectlSessionByID(ctx context.Context, id uuid.UUID) (sqlc.KubectlSession, error)
	ListActiveKubectlSessionsByCluster(ctx context.Context, clusterID uuid.UUID) ([]sqlc.KubectlSession, error)
	ListAllActiveKubectlSessions(ctx context.Context) ([]sqlc.KubectlSession, error)
	ListExpiredKubectlSessions(ctx context.Context) ([]sqlc.KubectlSession, error)
	SetKubectlSessionStatus(ctx context.Context, arg sqlc.SetKubectlSessionStatusParams) error
	TouchKubectlSessionInput(ctx context.Context, id uuid.UUID) error
	InsertKubectlSessionCommand(ctx context.Context, arg sqlc.InsertKubectlSessionCommandParams) error
	ListKubectlSessionCommands(ctx context.Context, arg sqlc.ListKubectlSessionCommandsParams) ([]sqlc.KubectlSessionCommand, error)
	CountKubectlSessionCommands(ctx context.Context, sessionID uuid.UUID) (int64, error)
}

// Deps is the wiring bundle for the lifecycle functions. Built once at
// server startup, passed to Open/Close/Reap.
type Deps struct {
	Queries   SessionQuerier
	Requester K8sRequester
	// Image override. Empty means use DefaultImage.
	Image string
	// IdleTimeout overrides the 30-minute default (chart value
	// kubectlShell.idleTimeoutMinutes). 0 means default.
	IdleTimeout time.Duration
	// HardCap overrides the 4-hour default (chart value
	// kubectlShell.sessionHardCapHours). 0 means default.
	HardCap time.Duration
	// PodReadyTimeout overrides the 60s pod-ready wait used by Open.
	// 0 means default. Tests usually pin to a small value.
	PodReadyTimeout time.Duration
	// Log is the slog logger. Nil means slog.Default().
	Log *slog.Logger
	// Now is the clock; nil means time.Now (overridable for tests).
	Now func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d Deps) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}

// OpenRequest is the input bundle for Open(). Carried in the handler
// request body / context.
type OpenRequest struct {
	UserID    uuid.UUID
	ClusterID uuid.UUID
	Verbs     EffectiveVerbs
	ClientIP  *netip.Addr
	UserAgent string
}

// SessionInfo is the small wire-shape returned by Open() / Get().
type SessionInfo struct {
	ID             uuid.UUID `json:"id"`
	ClusterID      uuid.UUID `json:"cluster_id"`
	UserID         uuid.UUID `json:"user_id"`
	Status         string    `json:"status"`
	PodName        string    `json:"pod_name"`
	PodNamespace   string    `json:"pod_namespace"`
	Container      string    `json:"container"`
	StartedAt      time.Time `json:"started_at"`
	LastInputAt    time.Time `json:"last_input_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	IdleTimeoutSec int64     `json:"idle_timeout_seconds"`
	CommandCount   int64     `json:"command_count,omitempty"`
}

// ToSessionInfo converts an sqlc row into the wire shape.
func ToSessionInfo(row sqlc.KubectlSession, commandCount int64, idle time.Duration) SessionInfo {
	return SessionInfo{
		ID:             row.ID,
		ClusterID:      row.ClusterID,
		UserID:         row.UserID,
		Status:         row.Status,
		PodName:        row.PodName,
		PodNamespace:   row.PodNamespace,
		Container:      ContainerName,
		StartedAt:      row.StartedAt,
		LastInputAt:    row.LastInputAt,
		ExpiresAt:      row.ExpiresAt,
		IdleTimeoutSec: int64(idle.Seconds()),
		CommandCount:   commandCount,
	}
}

// Open creates a new session row and provisions the in-cluster
// scaffolding: ServiceAccount → ClusterRole → ClusterRoleBinding → Pod,
// then waits up to PodReadyTimeout for the pod to report Ready.
//
// Failure mode: any step after the DB row is created flips the row to
// status=failed and best-effort tears down whatever k8s objects did
// land. The caller sees an error and the reaper later cleans up any
// straggling objects via the orphan-pod sweep.
func Open(ctx context.Context, deps Deps, req OpenRequest) (*SessionInfo, error) {
	if deps.Queries == nil {
		return nil, errors.New("kubectl: queries not configured")
	}
	if deps.Requester == nil {
		return nil, errors.New("kubectl: k8s requester not configured")
	}

	names := NewNames()

	row, err := deps.Queries.CreateKubectlSession(ctx, sqlc.CreateKubectlSessionParams{
		UserID:       req.UserID,
		ClusterID:    req.ClusterID,
		SaNamespace:  names.SANamespace,
		SaName:       names.SAName,
		PodNamespace: names.PodNamespace,
		PodName:      names.PodName,
		Status:       "starting",
		ClientIP:     req.ClientIP,
		UserAgent:    req.UserAgent,
	})
	if err != nil {
		return nil, fmt.Errorf("kubectl: create session row: %w", err)
	}

	clusterID := req.ClusterID.String()

	// Best-effort teardown helper used by every failure path below.
	fail := func(stepErr error) (*SessionInfo, error) {
		_ = deps.Queries.SetKubectlSessionStatus(ctx, sqlc.SetKubectlSessionStatusParams{
			ID:        row.ID,
			Status:    "failed",
			LastError: pgtype.Text{String: stepErr.Error(), Valid: true},
		})
		_ = tearDownK8s(ctx, deps, clusterID, names)
		return nil, stepErr
	}

	// Step 1: ServiceAccount.
	if err := applyJSON(ctx, deps.Requester, clusterID,
		fmt.Sprintf("/api/v1/namespaces/%s/serviceaccounts", names.SANamespace),
		ServiceAccountManifest(names)); err != nil {
		return fail(fmt.Errorf("create ServiceAccount: %w", err))
	}

	// Step 2: ClusterRole (skipped for superuser — they bind directly
	// to the built-in cluster-admin role).
	if !req.Verbs.Superuser {
		if err := applyJSON(ctx, deps.Requester, clusterID,
			"/apis/rbac.authorization.k8s.io/v1/clusterroles",
			ClusterRoleManifest(names, req.Verbs)); err != nil {
			return fail(fmt.Errorf("create ClusterRole: %w", err))
		}
	}

	// Step 3: ClusterRoleBinding.
	if err := applyJSON(ctx, deps.Requester, clusterID,
		"/apis/rbac.authorization.k8s.io/v1/clusterrolebindings",
		ClusterRoleBindingManifest(names, req.Verbs)); err != nil {
		return fail(fmt.Errorf("create ClusterRoleBinding: %w", err))
	}

	// Step 4: Pod.
	if err := applyJSON(ctx, deps.Requester, clusterID,
		fmt.Sprintf("/api/v1/namespaces/%s/pods", names.PodNamespace),
		PodManifest(names, deps.Image)); err != nil {
		return fail(fmt.Errorf("create Pod: %w", err))
	}

	// Step 5: wait for pod Ready.
	if err := waitForPodReady(ctx, deps, clusterID, names); err != nil {
		return fail(fmt.Errorf("wait pod ready: %w", err))
	}

	// Flip to active.
	if err := deps.Queries.SetKubectlSessionStatus(ctx, sqlc.SetKubectlSessionStatusParams{
		ID:     row.ID,
		Status: "active",
	}); err != nil {
		return fail(fmt.Errorf("mark active: %w", err))
	}

	info := ToSessionInfo(row, 0, deps.idleTimeout())
	info.Status = "active"
	return &info, nil
}

// Close tears down k8s objects for a session and flips the row to
// status=closed. Idempotent: repeated calls are no-ops once the row is
// already in a terminal state.
func Close(ctx context.Context, deps Deps, sessionID uuid.UUID) error {
	if deps.Queries == nil {
		return errors.New("kubectl: queries not configured")
	}
	row, err := deps.Queries.GetKubectlSessionByID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("kubectl: load session: %w", err)
	}
	if row.Status == "closed" || row.Status == "expired" || row.Status == "failed" {
		// Idempotent. Still try to scrub k8s objects in case the
		// previous Close didn't get to them.
		_ = tearDownK8s(ctx, deps, row.ClusterID.String(), namesFromRow(row))
		return nil
	}
	_ = tearDownK8s(ctx, deps, row.ClusterID.String(), namesFromRow(row))

	return deps.Queries.SetKubectlSessionStatus(ctx, sqlc.SetKubectlSessionStatusParams{
		ID:     sessionID,
		Status: "closed",
	})
}

// Reap closes idle / hard-capped sessions and sweeps orphaned debug
// pods in the cluster (pods matching astro-shell-* without a backing
// active DB row).
//
// Idle ⇒ status='expired'. Hard cap ⇒ status='expired'. Orphan pods
// without a DB row ⇒ best-effort DELETE.
func Reap(ctx context.Context, deps Deps) error {
	if deps.Queries == nil {
		return errors.New("kubectl: queries not configured")
	}
	expired, err := deps.Queries.ListExpiredKubectlSessions(ctx)
	if err != nil {
		return fmt.Errorf("list expired: %w", err)
	}
	idle := deps.idleTimeout()
	hard := deps.hardCap()
	now := deps.now()

	for _, row := range expired {
		isHard := !row.ExpiresAt.After(now)
		isIdle := row.LastInputAt.Add(idle).Before(now)
		_ = hard

		_ = tearDownK8s(ctx, deps, row.ClusterID.String(), namesFromRow(row))
		_ = deps.Queries.SetKubectlSessionStatus(ctx, sqlc.SetKubectlSessionStatusParams{
			ID:     row.ID,
			Status: "expired",
		})

		// T6.065 — audit fan-out. Distinguish hard-cap vs idle in
		// the audit detail so on-call can see *why* a session ended
		// without correlating against the expires_at column.
		reason := "idle_timeout"
		switch {
		case isHard && !isIdle:
			reason = "hard_cap"
		case isHard && isIdle:
			reason = "hard_cap_and_idle"
		}
		if writer, ok := any(deps.Queries).(audit.Querier); ok && writer != nil {
			audit.Record(ctx, writer, audit.Event{
				Source:       "worker",
				Action:       "cluster.shell.session.expired",
				ResourceType: "kubectl_session",
				ResourceID:   row.ID.String(),
				Detail: map[string]any{
					"reason":      reason,
					"cluster_id":  row.ClusterID.String(),
					"pod_name":    row.PodName,
					"started_at":  row.StartedAt.UTC().Format(time.RFC3339),
					"expires_at":  row.ExpiresAt.UTC().Format(time.RFC3339),
					"last_input":  row.LastInputAt.UTC().Format(time.RFC3339),
				},
			})
		}
	}

	// Orphan sweep: enumerate active rows per cluster, then list pods
	// in kube-system with the astronomer.io/component=kubectl-shell
	// label and delete any not in the active set.
	active, err := deps.Queries.ListAllActiveKubectlSessions(ctx)
	if err != nil {
		return fmt.Errorf("list active: %w", err)
	}
	activeByCluster := map[string]map[string]struct{}{}
	for _, row := range active {
		c := row.ClusterID.String()
		if activeByCluster[c] == nil {
			activeByCluster[c] = map[string]struct{}{}
		}
		activeByCluster[c][row.PodName] = struct{}{}
	}

	// Best-effort across clusters we know about. The reaper task in
	// the worker layer iterates all clusters; this function does the
	// per-cluster work.
	for clusterID := range activeByCluster {
		if err := sweepOrphanPods(ctx, deps, clusterID, activeByCluster[clusterID]); err != nil {
			deps.log().Warn("orphan pod sweep failed",
				slog.String("cluster_id", clusterID),
				slog.String("error", err.Error()))
		}
	}
	// T6.065 — gauge update. `active` was loaded earlier and reflects
	// post-reap state because Reap flips expired rows to 'expired'
	// before the ListAllActive call below would re-fetch — but we
	// re-list here for correctness (in case sweepOrphanPods caused
	// indirect status changes elsewhere).
	postReap, lerr := deps.Queries.ListAllActiveKubectlSessions(ctx)
	if lerr == nil {
		observability.SetKubectlActiveSessions(len(postReap))
	}
	return nil
}

// SweepOrphanPodsForCluster is a single-cluster orphan sweep callable
// by the worker task (which iterates the cluster list separately).
func SweepOrphanPodsForCluster(ctx context.Context, deps Deps, clusterID string, activePodNames map[string]struct{}) error {
	return sweepOrphanPods(ctx, deps, clusterID, activePodNames)
}

// idleTimeout returns the configured idle expiry (default 30 minutes).
func (d Deps) idleTimeout() time.Duration {
	if d.IdleTimeout > 0 {
		return d.IdleTimeout
	}
	return 30 * time.Minute
}

// hardCap returns the configured hard cap (default 4 hours).
func (d Deps) hardCap() time.Duration {
	if d.HardCap > 0 {
		return d.HardCap
	}
	return 4 * time.Hour
}

// podReadyTimeout returns the pod-ready wait (default 60s).
func (d Deps) podReadyTimeout() time.Duration {
	if d.PodReadyTimeout > 0 {
		return d.PodReadyTimeout
	}
	return 60 * time.Second
}

// namesFromRow rehydrates a Names bundle from a persisted row.
func namesFromRow(row sqlc.KubectlSession) Names {
	return Names{
		SAName:       row.SaName,
		SANamespace:  row.SaNamespace,
		RoleName:     row.SaName, // we use the same id for SA + Role + Binding
		BindingName:  row.SaName,
		PodName:      row.PodName,
		PodNamespace: row.PodNamespace,
		Container:    ContainerName,
	}
}

// applyJSON POSTs a manifest. 2xx + 409 (already exists) are both
// acceptable — the manifest set is idempotent across retries.
func applyJSON(ctx context.Context, r K8sRequester, clusterID, path string, body []byte) error {
	resp, err := r.Do(ctx, clusterID, "POST", path, body, map[string]string{
		"Content-Type": "application/json",
	})
	if err != nil {
		return err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode == 409 {
		// Already exists from a previous Open() retry.
		return nil
	}
	return fmt.Errorf("apply %s: status %d body=%s", path, resp.StatusCode, truncate(string(resp.Body), 256))
}

// deleteJSON best-effort DELETE. 404 is treated as success (already
// gone) so Close() is idempotent.
func deleteJSON(ctx context.Context, r K8sRequester, clusterID, path string) error {
	resp, err := r.Do(ctx, clusterID, "DELETE", path, nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode == 404 {
		return nil
	}
	return fmt.Errorf("delete %s: status %d body=%s", path, resp.StatusCode, truncate(string(resp.Body), 256))
}

// tearDownK8s removes (in reverse order of creation) Pod → Binding →
// ClusterRole → ServiceAccount. Errors are logged and swallowed so a
// partial teardown still tries to remove every object.
func tearDownK8s(ctx context.Context, deps Deps, clusterID string, n Names) error {
	var firstErr error
	step := func(path string) {
		if err := deleteJSON(ctx, deps.Requester, clusterID, path); err != nil {
			deps.log().Warn("kubectl teardown step failed",
				slog.String("cluster_id", clusterID),
				slog.String("path", path),
				slog.String("error", err.Error()))
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	step(fmt.Sprintf("/api/v1/namespaces/%s/pods/%s", n.PodNamespace, n.PodName))
	step(fmt.Sprintf("/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/%s", n.BindingName))
	step(fmt.Sprintf("/apis/rbac.authorization.k8s.io/v1/clusterroles/%s", n.RoleName))
	step(fmt.Sprintf("/api/v1/namespaces/%s/serviceaccounts/%s", n.SANamespace, n.SAName))
	return firstErr
}

// waitForPodReady polls the apiserver every 500ms (or PodReadyTimeout/8,
// whichever is smaller) until the pod reports Running + Ready or the
// deadline elapses.
func waitForPodReady(ctx context.Context, deps Deps, clusterID string, n Names) error {
	deadline := deps.now().Add(deps.podReadyTimeout())
	tick := 500 * time.Millisecond
	if d := deps.podReadyTimeout() / 8; d > 0 && d < tick {
		tick = d
	}
	for {
		if deps.now().After(deadline) {
			return fmt.Errorf("pod %s/%s did not become Ready within %s",
				n.PodNamespace, n.PodName, deps.podReadyTimeout())
		}
		resp, err := deps.Requester.Do(ctx, clusterID, "GET",
			fmt.Sprintf("/api/v1/namespaces/%s/pods/%s", n.PodNamespace, n.PodName),
			nil, nil)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if podIsReady(resp.Body) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(tick):
		}
	}
}

// podIsReady parses a minimal subset of the v1.Pod status block.
func podIsReady(body []byte) bool {
	var p struct {
		Status struct {
			Phase      string `json:"phase"`
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return false
	}
	if p.Status.Phase != "Running" {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == "Ready" && c.Status == "True" {
			return true
		}
	}
	return false
}

// sweepOrphanPods lists astro-shell-* pods in kube-system and deletes
// any not in `keep`. The label selector matches what every manifest
// in this package stamps; namespace is the default kube-system bucket.
func sweepOrphanPods(ctx context.Context, deps Deps, clusterID string, keep map[string]struct{}) error {
	resp, err := deps.Requester.Do(ctx, clusterID, "GET",
		fmt.Sprintf("/api/v1/namespaces/%s/pods?labelSelector=astronomer.io/component=kubectl-shell",
			DefaultNamespace), nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("list pods: status %d", resp.StatusCode)
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(resp.Body, &list); err != nil {
		return fmt.Errorf("decode pod list: %w", err)
	}
	for _, p := range list.Items {
		if _, ok := keep[p.Metadata.Name]; ok {
			continue
		}
		// Only delete if the name matches our astro-shell-* prefix; the
		// label could theoretically be set by something else.
		if !strings.HasPrefix(p.Metadata.Name, "astro-shell-") {
			continue
		}
		_ = deleteJSON(ctx, deps.Requester, clusterID,
			fmt.Sprintf("/api/v1/namespaces/%s/pods/%s", p.Metadata.Namespace, p.Metadata.Name))
		// Same for the RBAC pair — they share the suffix.
		_ = deleteJSON(ctx, deps.Requester, clusterID,
			fmt.Sprintf("/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/%s", p.Metadata.Name))
		_ = deleteJSON(ctx, deps.Requester, clusterID,
			fmt.Sprintf("/apis/rbac.authorization.k8s.io/v1/clusterroles/%s", p.Metadata.Name))
		_ = deleteJSON(ctx, deps.Requester, clusterID,
			fmt.Sprintf("/api/v1/namespaces/%s/serviceaccounts/%s", DefaultNamespace, p.Metadata.Name))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
