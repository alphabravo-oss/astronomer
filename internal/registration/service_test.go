package registration

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeQuerier is the registration.Querier surface backed by an
// in-memory store. Sufficient for service-level state-machine tests
// without spinning Postgres.
type fakeQuerier struct {
	mu       sync.Mutex
	clusters map[uuid.UUID]*sqlc.ClusterRegistrationRecord
	steps    []sqlc.ClusterRegistrationStep
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{clusters: map[uuid.UUID]*sqlc.ClusterRegistrationRecord{}}
}

func (f *fakeQuerier) seed(id uuid.UUID, phase Phase, baseline *bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := &sqlc.ClusterRegistrationRecord{
		ID:                id,
		RegistrationPhase: string(phase),
	}
	if baseline != nil {
		rec.InstallBaseline = pgtype.Bool{Bool: *baseline, Valid: true}
	}
	f.clusters[id] = rec
}

func (f *fakeQuerier) GetClusterRegistrationRecord(ctx context.Context, id uuid.UUID) (sqlc.ClusterRegistrationRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.clusters[id]
	if !ok {
		return sqlc.ClusterRegistrationRecord{}, pgx.ErrNoRows
	}
	return *r, nil
}

func (f *fakeQuerier) UpdateClusterRegistrationPhase(ctx context.Context, arg sqlc.UpdateClusterRegistrationPhaseParams) (sqlc.UpdateClusterRegistrationPhaseRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.clusters[arg.ID]
	if !ok {
		return sqlc.UpdateClusterRegistrationPhaseRow{}, pgx.ErrNoRows
	}
	r.RegistrationPhase = arg.RegistrationPhase
	if !r.RegistrationStartedAt.Valid && arg.RegistrationStartedAt.Valid {
		r.RegistrationStartedAt = arg.RegistrationStartedAt
	}
	r.RegistrationCompletedAt = arg.RegistrationCompletedAt
	return sqlc.UpdateClusterRegistrationPhaseRow{
		ID:                      r.ID,
		RegistrationPhase:       r.RegistrationPhase,
		RegistrationStartedAt:   r.RegistrationStartedAt,
		RegistrationCompletedAt: r.RegistrationCompletedAt,
		InstallBaseline:         r.InstallBaseline,
	}, nil
}

func (f *fakeQuerier) SetClusterInstallBaseline(ctx context.Context, arg sqlc.SetClusterInstallBaselineParams) (sqlc.SetClusterInstallBaselineRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.clusters[arg.ID]
	if !ok {
		return sqlc.SetClusterInstallBaselineRow{}, pgx.ErrNoRows
	}
	r.InstallBaseline = arg.InstallBaseline
	return sqlc.SetClusterInstallBaselineRow{
		ID:                      r.ID,
		RegistrationPhase:       r.RegistrationPhase,
		RegistrationStartedAt:   r.RegistrationStartedAt,
		RegistrationCompletedAt: r.RegistrationCompletedAt,
		InstallBaseline:         r.InstallBaseline,
	}, nil
}

func (f *fakeQuerier) InsertClusterRegistrationStep(ctx context.Context, arg sqlc.InsertClusterRegistrationStepParams) (sqlc.ClusterRegistrationStep, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	step := sqlc.ClusterRegistrationStep{
		ID:           uuid.New(),
		ClusterID:    arg.ClusterID,
		StepName:     arg.StepName,
		Label:        arg.Label,
		Status:       arg.Status,
		ProgressPct:  arg.ProgressPct,
		DetailJson:   arg.DetailJson,
		StartedAt:    arg.StartedAt,
		CompletedAt:  arg.CompletedAt,
		ErrorMessage: arg.ErrorMessage,
		CreatedAt:    time.Now().UTC(),
		StepOrder:    arg.StepOrder,
	}
	f.steps = append(f.steps, step)
	return step, nil
}

func (f *fakeQuerier) UpdateClusterRegistrationStep(ctx context.Context, arg sqlc.UpdateClusterRegistrationStepParams) (sqlc.ClusterRegistrationStep, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.steps {
		if f.steps[i].ID == arg.ID {
			f.steps[i].Status = arg.Status
			f.steps[i].ProgressPct = arg.ProgressPct
			if len(arg.DetailJson) > 0 {
				f.steps[i].DetailJson = arg.DetailJson
			}
			if !f.steps[i].StartedAt.Valid && arg.StartedAt.Valid {
				f.steps[i].StartedAt = arg.StartedAt
			}
			f.steps[i].CompletedAt = arg.CompletedAt
			f.steps[i].ErrorMessage = arg.ErrorMessage
			return f.steps[i], nil
		}
	}
	return sqlc.ClusterRegistrationStep{}, errors.New("not found")
}

func (f *fakeQuerier) ListClusterRegistrationSteps(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ClusterRegistrationStep, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.ClusterRegistrationStep{}
	for _, s := range f.steps {
		if s.ClusterID == clusterID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeQuerier) GetClusterRegistrationStep(ctx context.Context, id uuid.UUID) (sqlc.ClusterRegistrationStep, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.steps {
		if s.ID == id {
			return s, nil
		}
	}
	return sqlc.ClusterRegistrationStep{}, pgx.ErrNoRows
}

func (f *fakeQuerier) CloseRunningStepsForCluster(ctx context.Context, arg sqlc.CloseRunningStepsForClusterParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, s := range f.steps {
		if s.ClusterID == arg.ClusterID && s.StepName == arg.StepName && s.Status == "running" {
			f.steps[i].Status = "failed"
			if !f.steps[i].CompletedAt.Valid {
				f.steps[i].CompletedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			}
			if f.steps[i].ErrorMessage == "" {
				f.steps[i].ErrorMessage = "superseded by retry"
			}
		}
	}
	return nil
}

func (f *fakeQuerier) MaxStepOrderForCluster(ctx context.Context, clusterID uuid.UUID) (int32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var max int32
	for _, s := range f.steps {
		if s.ClusterID == clusterID && s.StepOrder > max {
			max = s.StepOrder
		}
	}
	return max, nil
}

// capturingPublisher records every Publish call so tests can assert
// that SSE fan-out fires on transitions / step writes.
type capturingPublisher struct {
	mu     sync.Mutex
	events []struct {
		Type string
		Data any
	}
}

func (p *capturingPublisher) Publish(t string, d any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, struct {
		Type string
		Data any
	}{t, d})
}

func (p *capturingPublisher) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.events))
	for i, e := range p.events {
		out[i] = e.Type
	}
	return out
}

// TestRegistrationWizard_OptionsRoundtrip — PUT options writes
// install_baseline, GET status reads it back.
func TestRegistrationWizard_OptionsRoundtrip(t *testing.T) {
	q := newFakeQuerier()
	id := uuid.New()
	q.seed(id, PhaseCreated, nil)
	svc := New(q, nil)

	rec, err := svc.SetInstallBaseline(context.Background(), id, true)
	if err != nil {
		t.Fatalf("set baseline: %v", err)
	}
	if !rec.InstallBaseline.Valid || !rec.InstallBaseline.Bool {
		t.Fatalf("install_baseline not set: %+v", rec.InstallBaseline)
	}

	status, err := svc.LoadStatus(context.Background(), id)
	if err != nil {
		t.Fatalf("load status: %v", err)
	}
	if status.InstallBaseline == nil || !*status.InstallBaseline {
		t.Fatalf("status.InstallBaseline = %v, want true", status.InstallBaseline)
	}
	// Opt out and confirm the flag flips.
	if _, err := svc.SetInstallBaseline(context.Background(), id, false); err != nil {
		t.Fatalf("clear baseline: %v", err)
	}
	status, _ = svc.LoadStatus(context.Background(), id)
	if status.InstallBaseline == nil || *status.InstallBaseline {
		t.Fatalf("status.InstallBaseline = %v, want false", status.InstallBaseline)
	}
}

// TestRegistrationWizard_ConfirmAdvancesPhase verifies that
// Advance(EventConfirm) moves the cluster created → awaiting_agent.
func TestRegistrationWizard_ConfirmAdvancesPhase(t *testing.T) {
	q := newFakeQuerier()
	id := uuid.New()
	q.seed(id, PhaseCreated, nil)
	svc := New(q, nil)

	rec, err := svc.Advance(context.Background(), id, EventConfirm)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if rec.RegistrationPhase != string(PhaseAwaitingAgent) {
		t.Fatalf("want awaiting_agent, got %s", rec.RegistrationPhase)
	}
	if !rec.RegistrationStartedAt.Valid {
		t.Fatal("registration_started_at should be stamped on first transition")
	}
}

// TestRegistrationWizard_AgentConnectAdvancesPhase — the hub-side hook
// transitions awaiting_agent → connected and writes the
// agent_connected step row.
//
// This cluster has no baseline choice recorded (seed passes nil), so nothing is
// scheduled to provision and the handshake carries it on through to ready via
// EventNoProvisioning; `connected` is a transient stop. The hook's own job —
// the transition off awaiting_agent and the agent_connected step row, with the
// agent version in its detail — is what this asserts.
func TestRegistrationWizard_AgentConnectAdvancesPhase(t *testing.T) {
	q := newFakeQuerier()
	id := uuid.New()
	q.seed(id, PhaseAwaitingAgent, nil)
	pub := &capturingPublisher{}
	svc := New(q, pub)

	if err := svc.OnAgentConnected(context.Background(), id, "v1.2.3"); err != nil {
		t.Fatalf("OnAgentConnected: %v", err)
	}
	rec, _ := q.GetClusterRegistrationRecord(context.Background(), id)
	if rec.RegistrationPhase != string(PhaseReady) {
		t.Fatalf("want ready, got %s", rec.RegistrationPhase)
	}
	steps, _ := q.ListClusterRegistrationSteps(context.Background(), id)
	if len(steps) != 2 || steps[0].StepName != "agent_connected" || steps[1].StepName != "no_provisioning" {
		t.Fatalf("expected agent_connected then no_provisioning steps, got %#v", steps)
	}
	// Detail should contain agent_version.
	var detail map[string]any
	if err := json.Unmarshal(steps[0].DetailJson, &detail); err != nil {
		t.Fatalf("detail parse: %v", err)
	}
	if detail["agent_version"] != "v1.2.3" {
		t.Errorf("want agent_version v1.2.3, got %v", detail["agent_version"])
	}
}

// TestRegistrationWizard_NoBaselineSkipsProvisioning — when the
// operator opted out, OnAgentConnected lands on `ready`, not
// `connected`.
func TestRegistrationWizard_NoBaselineSkipsProvisioning(t *testing.T) {
	q := newFakeQuerier()
	id := uuid.New()
	optedOut := false
	q.seed(id, PhaseAwaitingAgent, &optedOut)
	svc := New(q, nil)

	if err := svc.OnAgentConnected(context.Background(), id, "v8844890"); err != nil {
		t.Fatalf("OnAgentConnected: %v", err)
	}
	rec, _ := q.GetClusterRegistrationRecord(context.Background(), id)
	if rec.RegistrationPhase != string(PhaseReady) {
		t.Fatalf("want ready, got %s", rec.RegistrationPhase)
	}
}

// TestRegistrationWizard_TemplateApplyAdvancesPhase — the apply-worker
// hooks march the cluster connected → provisioning → ready and write
// the corresponding step rows.
func TestRegistrationWizard_TemplateApplyAdvancesPhase(t *testing.T) {
	q := newFakeQuerier()
	id := uuid.New()
	yes := true
	q.seed(id, PhaseConnected, &yes)
	svc := New(q, nil)

	if err := svc.OnTemplateApplyStart(context.Background(), id); err != nil {
		t.Fatalf("apply-start: %v", err)
	}
	rec, _ := q.GetClusterRegistrationRecord(context.Background(), id)
	if rec.RegistrationPhase != string(PhaseProvisioning) {
		t.Fatalf("after start: want provisioning, got %s", rec.RegistrationPhase)
	}
	if err := svc.OnTemplateApplySuccess(context.Background(), id); err != nil {
		t.Fatalf("apply-success: %v", err)
	}
	rec, _ = q.GetClusterRegistrationRecord(context.Background(), id)
	if rec.RegistrationPhase != string(PhaseReady) {
		t.Fatalf("after success: want ready, got %s", rec.RegistrationPhase)
	}
	if !rec.RegistrationCompletedAt.Valid {
		t.Error("registration_completed_at should be stamped on terminal phase")
	}
	steps, _ := q.ListClusterRegistrationSteps(context.Background(), id)
	wantNames := []string{"template_applying", "template_applied"}
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d (%#v)", len(steps), steps)
	}
	for i, w := range wantNames {
		if steps[i].StepName != w {
			t.Errorf("step %d: name = %s, want %s", i, steps[i].StepName, w)
		}
	}
}

// TestRegistrationWizard_TemplateApplyFailure exercises the failure
// edge so the operator's retry button has something to retry against.
func TestRegistrationWizard_TemplateApplyFailureAdvancesToFailed(t *testing.T) {
	q := newFakeQuerier()
	id := uuid.New()
	yes := true
	q.seed(id, PhaseProvisioning, &yes)
	svc := New(q, nil)

	if err := svc.OnTemplateApplyFailure(context.Background(), id, "ImagePullBackOff"); err != nil {
		t.Fatalf("apply-failure: %v", err)
	}
	rec, _ := q.GetClusterRegistrationRecord(context.Background(), id)
	if rec.RegistrationPhase != string(PhaseFailed) {
		t.Fatalf("want failed, got %s", rec.RegistrationPhase)
	}
	steps, _ := q.ListClusterRegistrationSteps(context.Background(), id)
	if len(steps) != 1 || steps[0].StepName != "template_failed" {
		t.Fatalf("expected one template_failed step, got %#v", steps)
	}
	if steps[0].ErrorMessage != "ImagePullBackOff" {
		t.Errorf("error_message lost in translation: %q", steps[0].ErrorMessage)
	}
}

// TestRegistrationWizard_SSEStreamEmitsStepEvents — every step write
// publishes a cluster.registration.step event, every phase change
// publishes a cluster.registration.phase event. The SSE handler
// downstream filters by cluster_id.
func TestRegistrationWizard_SSEStreamEmitsStepEvents(t *testing.T) {
	q := newFakeQuerier()
	id := uuid.New()
	q.seed(id, PhaseAwaitingAgent, nil)
	pub := &capturingPublisher{}
	svc := New(q, pub)

	if err := svc.OnAgentConnected(context.Background(), id, "v1"); err != nil {
		t.Fatalf("OnAgentConnected: %v", err)
	}

	gotTypes := pub.snapshot()
	hasPhase := false
	hasStep := false
	for _, t := range gotTypes {
		if t == "cluster.registration.phase" {
			hasPhase = true
		}
		if t == "cluster.registration.step" {
			hasStep = true
		}
	}
	if !hasPhase {
		t.Errorf("missing cluster.registration.phase event, got %#v", gotTypes)
	}
	if !hasStep {
		t.Errorf("missing cluster.registration.step event, got %#v", gotTypes)
	}
}

// A cluster attached outside the wizard (a raw `kubectl apply` of the agent
// manifest) never records a baseline choice, so install_baseline stays NULL and
// no template apply is ever scheduled. Nothing will ever deliver
// EventTemplateApplied, so if the connect handshake doesn't advance it, the
// cluster sits at `connected` forever — healthy on every condition, but wearing
// a warning badge and never reaching ready.
func TestOnAgentConnected_NullInstallBaselineReachesReady(t *testing.T) {
	for _, tc := range []struct {
		name     string
		baseline *bool
		want     Phase
	}{
		{"never chosen (attached outside the wizard)", nil, PhaseReady},
		{"explicitly opted out", boolPtr(false), PhaseReady},
		{"opted in — an apply is coming, so wait for it", boolPtr(true), PhaseConnected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := newFakeQuerier()
			id := uuid.New()
			q.seed(id, PhaseAwaitingAgent, tc.baseline)
			svc := New(q, nil)

			if err := svc.OnAgentConnected(context.Background(), id, "v1.2.3"); err != nil {
				t.Fatalf("OnAgentConnected: %v", err)
			}
			rec, err := q.GetClusterRegistrationRecord(context.Background(), id)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got := Phase(rec.RegistrationPhase); got != tc.want {
				t.Fatalf("phase = %s, want %s", got, tc.want)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }
