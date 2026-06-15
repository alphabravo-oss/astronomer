package registration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Querier is the DB surface the Service needs. Declared as an
// interface so tests can stand up an in-memory fake without spinning
// Postgres. *sqlc.Queries satisfies it natively.
type Querier interface {
	GetClusterRegistrationRecord(ctx context.Context, id uuid.UUID) (sqlc.ClusterRegistrationRecord, error)
	UpdateClusterRegistrationPhase(ctx context.Context, arg sqlc.UpdateClusterRegistrationPhaseParams) (sqlc.UpdateClusterRegistrationPhaseRow, error)
	SetClusterInstallBaseline(ctx context.Context, arg sqlc.SetClusterInstallBaselineParams) (sqlc.SetClusterInstallBaselineRow, error)
	InsertClusterRegistrationStep(ctx context.Context, arg sqlc.InsertClusterRegistrationStepParams) (sqlc.ClusterRegistrationStep, error)
	UpdateClusterRegistrationStep(ctx context.Context, arg sqlc.UpdateClusterRegistrationStepParams) (sqlc.ClusterRegistrationStep, error)
	ListClusterRegistrationSteps(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ClusterRegistrationStep, error)
	GetClusterRegistrationStep(ctx context.Context, id uuid.UUID) (sqlc.ClusterRegistrationStep, error)
	MaxStepOrderForCluster(ctx context.Context, clusterID uuid.UUID) (int32, error)
	CloseRunningStepsForCluster(ctx context.Context, arg sqlc.CloseRunningStepsForClusterParams) error
}

func recordFromPhaseRow(row sqlc.UpdateClusterRegistrationPhaseRow) sqlc.ClusterRegistrationRecord {
	return sqlc.ClusterRegistrationRecord(row)
}

func recordFromBaselineRow(row sqlc.SetClusterInstallBaselineRow) sqlc.ClusterRegistrationRecord {
	return sqlc.ClusterRegistrationRecord(row)
}

// Publisher is the SSE fan-out surface the service uses. *events.Bus
// implements this naturally. Optional / nil-safe: when not wired,
// transitions still persist but no SSE event lands.
type Publisher interface {
	Publish(eventType string, data any)
}

// Service wraps the phase machine + step writes + SSE publish into a
// single helper. Three callers reach for it:
//
//   - The cluster handler (POST /clusters/, PUT /options/, POST /confirm/,
//     POST /retry/, POST /cancel/)
//   - The tunnel hub on first agent.connected
//   - The cluster_template:apply task on start/per-tool/end
//
// Centralising the logic means each caller doesn't have to remember to
// write a step row + publish an event after every DB write — they
// invoke one Service method.
type Service struct {
	q   Querier
	pub Publisher
	// metrics is an optional hook for the Prometheus gauges. Set via
	// SetMetricsHook; nil-safe.
	metrics MetricsHook
}

// MetricsHook is the bridge to Prometheus. The handler / wiring layer
// implements this in the metrics package; declared here as an
// interface to avoid a transitive import cycle (the metrics package
// depends on tasks, tasks depend on registration).
type MetricsHook interface {
	RecordPhaseTransition(clusterID, from, to string)
	RecordDuration(clusterID, outcome string, baseline bool, seconds float64)
}

// New constructs a Service. pub may be nil.
func New(q Querier, pub Publisher) *Service {
	return &Service{q: q, pub: pub}
}

// SetMetricsHook wires the Prometheus bridge. Optional.
func (s *Service) SetMetricsHook(m MetricsHook) {
	if s == nil {
		return
	}
	s.metrics = m
}

// ErrNotFound mirrors pgx.ErrNoRows for callers that don't want to
// import pgx just to handle the missing-row case.
var ErrNotFound = errors.New("cluster registration not found")

// stepWriteResult is the payload published to the SSE bus when a step
// is created or updated. Mirrors the protocol shape called out in the
// sprint plan.
type stepWriteResult struct {
	ClusterID   uuid.UUID       `json:"cluster_id"`
	StepID      uuid.UUID       `json:"step_id"`
	StepName    string          `json:"step_name"`
	Label       string          `json:"label"`
	Status      string          `json:"status"`
	Progress    int             `json:"progress"`
	Detail      json.RawMessage `json:"detail,omitempty"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	Error       string          `json:"error,omitempty"`
	StepOrder   int             `json:"step_order"`
}

// phaseChangeResult is published when the cluster's phase column moves.
type phaseChangeResult struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
}

// StepInput is the inbound shape used by Service.WriteStep. Fields are
// optional except StepName + Status; Service fills in label, order,
// timestamps as needed.
type StepInput struct {
	StepName     string
	Status       string
	ProgressPct  int32
	Detail       map[string]any
	ErrorMessage string
	// MarkStarted forces started_at = now() even when Status != "running".
	MarkStarted bool
	// MarkCompleted forces completed_at = now() even when Status != success/failed/skipped.
	MarkCompleted bool
}

// WriteStep appends a new step row. Used at lifecycle boundaries the
// caller hasn't seen before (cluster_created, agent_connected, …).
// UpdateStep is the counterpart for in-flight mutations (tool install
// progress).
func (s *Service) WriteStep(ctx context.Context, clusterID uuid.UUID, in StepInput) (sqlc.ClusterRegistrationStep, error) {
	if s == nil || s.q == nil {
		return sqlc.ClusterRegistrationStep{}, fmt.Errorf("registration service not configured")
	}
	order, _ := s.q.MaxStepOrderForCluster(ctx, clusterID)
	order++
	var startedAt, completedAt pgtype.Timestamptz
	now := time.Now().UTC()
	if in.MarkStarted || in.Status == "running" {
		startedAt = pgtype.Timestamptz{Time: now, Valid: true}
	}
	if in.MarkCompleted || in.Status == "success" || in.Status == "failed" || in.Status == "skipped" {
		completedAt = pgtype.Timestamptz{Time: now, Valid: true}
	}
	detail := json.RawMessage(`{}`)
	if len(in.Detail) > 0 {
		b, err := json.Marshal(in.Detail)
		if err == nil {
			detail = b
		}
	}
	progress := in.ProgressPct
	if in.Status == "success" || in.Status == "skipped" {
		progress = 100
	}
	step, err := s.q.InsertClusterRegistrationStep(ctx, sqlc.InsertClusterRegistrationStepParams{
		ClusterID:    clusterID,
		StepName:     in.StepName,
		Label:        StepLabel(in.StepName),
		Status:       in.Status,
		ProgressPct:  progress,
		DetailJson:   detail,
		StartedAt:    startedAt,
		CompletedAt:  completedAt,
		ErrorMessage: in.ErrorMessage,
		StepOrder:    order,
	})
	if err != nil {
		return sqlc.ClusterRegistrationStep{}, err
	}
	s.publishStep(step)
	return step, nil
}

// UpdateStep mutates an existing step row by ID. Used by the
// cluster_template:apply worker to advance progress on a long-running
// install without writing a fresh row per tick.
type UpdateStepInput struct {
	StepID       uuid.UUID
	Status       string
	ProgressPct  int32
	Detail       map[string]any
	ErrorMessage string
}

func (s *Service) UpdateStep(ctx context.Context, in UpdateStepInput) (sqlc.ClusterRegistrationStep, error) {
	if s == nil || s.q == nil {
		return sqlc.ClusterRegistrationStep{}, fmt.Errorf("registration service not configured")
	}
	var startedAt, completedAt pgtype.Timestamptz
	now := time.Now().UTC()
	if in.Status == "running" {
		startedAt = pgtype.Timestamptz{Time: now, Valid: true}
	}
	if in.Status == "success" || in.Status == "failed" || in.Status == "skipped" {
		completedAt = pgtype.Timestamptz{Time: now, Valid: true}
	}
	progress := in.ProgressPct
	if in.Status == "success" || in.Status == "skipped" {
		progress = 100
	}
	var detail json.RawMessage
	if len(in.Detail) > 0 {
		if b, err := json.Marshal(in.Detail); err == nil {
			detail = b
		}
	}
	step, err := s.q.UpdateClusterRegistrationStep(ctx, sqlc.UpdateClusterRegistrationStepParams{
		ID:           in.StepID,
		Status:       in.Status,
		ProgressPct:  progress,
		DetailJson:   detail,
		StartedAt:    startedAt,
		CompletedAt:  completedAt,
		ErrorMessage: in.ErrorMessage,
	})
	if err != nil {
		return sqlc.ClusterRegistrationStep{}, err
	}
	s.publishStep(step)
	return step, nil
}

// Advance runs the phase machine for one event. Returns the new
// (possibly identical) record. Publishes a phase change event and
// writes any auto-generated step rows the transition implies
// (e.g. EventNoProvisioning writes a 'no_provisioning' step,
// EventAgentConnected writes an 'agent_connected' step).
//
// The function is best-effort idempotent: re-applying the same event
// when the cluster is already in the target phase is a no-op (no DB
// write, no event).
func (s *Service) Advance(ctx context.Context, clusterID uuid.UUID, ev Event, opts ...AdvanceOption) (sqlc.ClusterRegistrationRecord, error) {
	if s == nil || s.q == nil {
		return sqlc.ClusterRegistrationRecord{}, fmt.Errorf("registration service not configured")
	}
	o := advanceOpts{}
	for _, fn := range opts {
		fn(&o)
	}

	record, err := s.q.GetClusterRegistrationRecord(ctx, clusterID)
	if err != nil {
		return sqlc.ClusterRegistrationRecord{}, err
	}

	baseline := record.InstallBaseline.Valid && record.InstallBaseline.Bool
	next, terr := Transition(Phase(record.RegistrationPhase), ev, baseline)
	if terr != nil {
		return record, terr
	}
	if next == Phase(record.RegistrationPhase) {
		// No-op transition (e.g. heartbeat after first connect).
		return record, nil
	}

	var startedAt, completedAt pgtype.Timestamptz
	now := time.Now().UTC()
	// Stamp registration_started_at on the FIRST move off `created`
	// — the COALESCE in the UPDATE statement makes this idempotent
	// on later transitions.
	if record.RegistrationPhase == string(PhaseCreated) {
		startedAt = pgtype.Timestamptz{Time: now, Valid: true}
	}
	if IsTerminal(next) {
		completedAt = pgtype.Timestamptz{Time: now, Valid: true}
	} else {
		// Don't bash an existing completed_at; pass the record's
		// current value. pgtype zero-value is invalid which
		// translates to SQL NULL in this update.
		completedAt = record.RegistrationCompletedAt
		if next != PhaseFailed && record.RegistrationCompletedAt.Valid {
			// Re-entering a non-terminal state from a terminal
			// one (e.g. retry) clears completed_at so the UI
			// shows the timer running again.
			completedAt = pgtype.Timestamptz{}
		}
	}

	updated, err := s.q.UpdateClusterRegistrationPhase(ctx, sqlc.UpdateClusterRegistrationPhaseParams{
		ID:                      clusterID,
		RegistrationPhase:       string(next),
		RegistrationStartedAt:   startedAt,
		RegistrationCompletedAt: completedAt,
	})
	if err != nil {
		return record, err
	}
	updatedRecord := recordFromPhaseRow(updated)

	// Side-effect step rows. Skip when the caller has already written
	// the step themselves (cluster_template:apply writes a tool-
	// specific row + then advances).
	if !o.skipAutoStep {
		switch ev {
		case EventAgentConnected:
			_, _ = s.WriteStep(ctx, clusterID, StepInput{
				StepName: "agent_connected",
				Status:   "success",
				Detail:   o.detail,
			})
		case EventNoProvisioning:
			_, _ = s.WriteStep(ctx, clusterID, StepInput{
				StepName: "no_provisioning",
				Status:   "skipped",
			})
		case EventCancel:
			_, _ = s.WriteStep(ctx, clusterID, StepInput{
				StepName:     "cancelled",
				Status:       "failed",
				ErrorMessage: o.errorMessage,
			})
		}
	}

	s.publishPhase(record.RegistrationPhase, string(next), clusterID)
	if s.metrics != nil {
		s.metrics.RecordPhaseTransition(clusterID.String(), record.RegistrationPhase, string(next))
		if IsTerminal(next) {
			started := updated.RegistrationStartedAt
			if started.Valid {
				dur := time.Since(started.Time).Seconds()
				outcome := "success"
				if next == PhaseFailed {
					outcome = "failed"
				}
				s.metrics.RecordDuration(clusterID.String(), outcome, baseline, dur)
			}
		}
	}
	return updatedRecord, nil
}

// AdvanceOption tweaks the side-effects of Advance. Used by the apply
// worker so its per-tool step rows aren't duplicated by the auto-step
// branch in Advance.
type AdvanceOption func(*advanceOpts)

type advanceOpts struct {
	skipAutoStep bool
	detail       map[string]any
	errorMessage string
}

// WithSkipAutoStep suppresses the side-effect step row. Used when the
// caller already wrote the step themselves before Advancing.
func WithSkipAutoStep() AdvanceOption {
	return func(o *advanceOpts) { o.skipAutoStep = true }
}

// WithDetail attaches a detail map to any auto-generated step row.
// Currently consumed by EventAgentConnected (agent_version).
func WithDetail(d map[string]any) AdvanceOption {
	return func(o *advanceOpts) { o.detail = d }
}

// WithError records an error message on any auto-generated step row.
// Used with EventCancel so superuser cancellations surface a reason.
func WithError(msg string) AdvanceOption {
	return func(o *advanceOpts) { o.errorMessage = msg }
}

// OnAgentConnected is the tunnel-hub hook. Called every time an agent
// completes the CONNECT_ACK handshake. The first heartbeat for a
// cluster in `awaiting_agent` advances it through `connected`, then
// either straight to `ready` (when install_baseline=false) or holds
// at `connected` waiting for the apply task (when install_baseline=true).
//
// Idempotent: subsequent heartbeats for a cluster already past
// `connected` are no-ops (the phase machine treats them as such).
func (s *Service) OnAgentConnected(ctx context.Context, clusterID uuid.UUID, agentVersion string) error {
	if s == nil || s.q == nil {
		return nil
	}
	rec, err := s.Advance(ctx, clusterID, EventAgentConnected, WithDetail(map[string]any{
		"agent_version": agentVersion,
	}))
	if err != nil {
		// Illegal transition (e.g. cluster already in `ready`) is
		// not a true error — log it elsewhere; return nil so the
		// connect handshake doesn't fail.
		if errors.Is(err, ErrIllegalTransition) {
			return nil
		}
		// String-match the wrapped variants from Transition.
		if msg := err.Error(); len(msg) >= 26 && msg[:26] == "illegal phase transition: " {
			return nil
		}
		return err
	}
	// If the operator opted out of baseline, head straight to ready.
	if rec.RegistrationPhase == string(PhaseConnected) {
		if rec.InstallBaseline.Valid && !rec.InstallBaseline.Bool {
			_, _ = s.Advance(ctx, clusterID, EventNoProvisioning)
		}
	}
	return nil
}

// OnTemplateApplyStart is the apply-worker hook on task start.
// Writes a `template_applying` step and advances the cluster's phase
// into `provisioning` from whatever terminal-ish state it was in.
//
// Two entry paths into "we're applying again":
//
//   - Happy path: phase is `connected` (operator just confirmed the
//     wizard). EventTemplateApplying transitions connected →
//     provisioning.
//
//   - Retry path: phase is `failed` (a previous apply failed and the
//     operator clicked reapply or the periodic recovery sweep
//     re-enqueued the task). The phase machine rejects
//     EventTemplateApplying from `failed` — we must first emit
//     EventRetry (failed → provisioning), then the apply-success path
//     will transition provisioning → ready cleanly.
//
// Idempotent / nil-safe; phase-machine ErrIllegalTransition is treated
// as a no-op so a double-fire (e.g. async retry race) doesn't error
// the caller.
func (s *Service) OnTemplateApplyStart(ctx context.Context, clusterID uuid.UUID) error {
	if s == nil || s.q == nil {
		return nil
	}
	// Close any prior `template_applying` row still marked running.
	// Without this, the orchestrator's auto-retry path leaves orphan
	// "running" timeline rows that never resolve — see sprint 086.
	_ = s.q.CloseRunningStepsForCluster(ctx, sqlc.CloseRunningStepsForClusterParams{
		ClusterID: clusterID,
		StepName:  "template_applying",
	})
	_, _ = s.WriteStep(ctx, clusterID, StepInput{
		StepName: "template_applying",
		Status:   "running",
	})

	// Recovery rewind: a cluster sitting in `failed` from a previous
	// apply needs to rewind to `provisioning` before the regular
	// applying-event will be accepted. Best-effort — if we can't read
	// the record we fall through and let the EventTemplateApplying
	// branch handle whatever illegal-transition the machine reports.
	if rec, rerr := s.q.GetClusterRegistrationRecord(ctx, clusterID); rerr == nil && rec.RegistrationPhase == string(PhaseFailed) {
		if _, err := s.Advance(ctx, clusterID, EventRetry, WithSkipAutoStep()); err != nil && !s.isIllegal(err) {
			return err
		}
		// Phase is now `provisioning`. EventTemplateApplying from
		// provisioning is illegal too (it expects `connected`), so
		// return early — the apply task's success/failure event will
		// drive the next transition from provisioning.
		return nil
	}

	if _, err := s.Advance(ctx, clusterID, EventTemplateApplying, WithSkipAutoStep()); err != nil {
		if s.isIllegal(err) {
			return nil
		}
		return err
	}
	return nil
}

// isIllegal returns true for the phase-machine's "this transition isn't
// allowed from the current state" error. Centralized so callers don't
// re-implement the two checks (errors.Is + string-prefix fallback for
// the wrapped variant) at every site.
func (s *Service) isIllegal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrIllegalTransition) {
		return true
	}
	msg := err.Error()
	return len(msg) >= 26 && msg[:26] == "illegal phase transition: "
}

// OnTemplateApplySuccess is the apply-worker hook on successful end.
// Writes a `template_applied` step and advances provisioning → ready.
func (s *Service) OnTemplateApplySuccess(ctx context.Context, clusterID uuid.UUID) error {
	if s == nil || s.q == nil {
		return nil
	}
	_, _ = s.WriteStep(ctx, clusterID, StepInput{
		StepName: "template_applied",
		Status:   "success",
	})
	if _, err := s.Advance(ctx, clusterID, EventTemplateApplied, WithSkipAutoStep()); err != nil {
		if errors.Is(err, ErrIllegalTransition) {
			return nil
		}
		if msg := err.Error(); len(msg) >= 26 && msg[:26] == "illegal phase transition: " {
			return nil
		}
		return err
	}
	return nil
}

// OnTemplateApplyFailure is the apply-worker hook on terminal failure.
// Writes a `template_failed` step and advances provisioning → failed.
func (s *Service) OnTemplateApplyFailure(ctx context.Context, clusterID uuid.UUID, errMsg string) error {
	if s == nil || s.q == nil {
		return nil
	}
	_, _ = s.WriteStep(ctx, clusterID, StepInput{
		StepName:     "template_failed",
		Status:       "failed",
		ErrorMessage: errMsg,
	})
	if _, err := s.Advance(ctx, clusterID, EventTemplateFailed, WithSkipAutoStep()); err != nil {
		if errors.Is(err, ErrIllegalTransition) {
			return nil
		}
		if msg := err.Error(); len(msg) >= 26 && msg[:26] == "illegal phase transition: " {
			return nil
		}
		return err
	}
	return nil
}

// SetInstallBaseline writes the operator's wizard-step-1 choice. NULL
// stays NULL when the operator hasn't decided yet; FALSE / TRUE are
// recorded as their explicit values.
func (s *Service) SetInstallBaseline(ctx context.Context, clusterID uuid.UUID, value bool) (sqlc.ClusterRegistrationRecord, error) {
	if s == nil || s.q == nil {
		return sqlc.ClusterRegistrationRecord{}, fmt.Errorf("registration service not configured")
	}
	row, err := s.q.SetClusterInstallBaseline(ctx, sqlc.SetClusterInstallBaselineParams{
		ID:              clusterID,
		InstallBaseline: pgtype.Bool{Bool: value, Valid: true},
	})
	if err != nil {
		return sqlc.ClusterRegistrationRecord{}, err
	}
	return recordFromBaselineRow(row), nil
}

// publishStep fans a step row out to SSE subscribers. Best-effort.
func (s *Service) publishStep(step sqlc.ClusterRegistrationStep) {
	if s == nil || s.pub == nil {
		return
	}
	payload := stepWriteResult{
		ClusterID: step.ClusterID,
		StepID:    step.ID,
		StepName:  step.StepName,
		Label:     step.Label,
		Status:    step.Status,
		Progress:  int(step.ProgressPct),
		Detail:    step.DetailJson,
		Error:     step.ErrorMessage,
		StepOrder: int(step.StepOrder),
	}
	if step.StartedAt.Valid {
		t := step.StartedAt.Time.UTC()
		payload.StartedAt = &t
	}
	if step.CompletedAt.Valid {
		t := step.CompletedAt.Time.UTC()
		payload.CompletedAt = &t
	}
	s.pub.Publish("cluster.registration.step", payload)
}

func (s *Service) publishPhase(from, to string, clusterID uuid.UUID) {
	if s == nil || s.pub == nil {
		return
	}
	s.pub.Publish("cluster.registration.phase", phaseChangeResult{
		ClusterID: clusterID,
		From:      from,
		To:        to,
	})
}

// Status is the wire-format payload for GET /registration/status/.
// Kept here (not handler) so tests can reuse the rendering helper.
type Status struct {
	ClusterID       uuid.UUID  `json:"cluster_id"`
	Phase           string     `json:"phase"`
	InstallBaseline *bool      `json:"install_baseline,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	Steps           []Step     `json:"steps"`
}

// Step is one timeline entry in the Status response.
type Step struct {
	ID           uuid.UUID       `json:"id"`
	StepName     string          `json:"step_name"`
	Label        string          `json:"label"`
	Status       string          `json:"status"`
	ProgressPct  int             `json:"progress_pct"`
	Detail       json.RawMessage `json:"detail,omitempty"`
	StartedAt    *time.Time      `json:"started_at,omitempty"`
	CompletedAt  *time.Time      `json:"completed_at,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	StepOrder    int             `json:"step_order"`
	CreatedAt    time.Time       `json:"created_at"`
}

// LoadStatus fetches the record + step list and renders the JSON shape.
func (s *Service) LoadStatus(ctx context.Context, clusterID uuid.UUID) (Status, error) {
	if s == nil || s.q == nil {
		return Status{}, fmt.Errorf("registration service not configured")
	}
	record, err := s.q.GetClusterRegistrationRecord(ctx, clusterID)
	if err != nil {
		return Status{}, err
	}
	rows, err := s.q.ListClusterRegistrationSteps(ctx, clusterID)
	if err != nil {
		return Status{}, err
	}
	out := Status{
		ClusterID: clusterID,
		Phase:     record.RegistrationPhase,
		Steps:     make([]Step, 0, len(rows)),
	}
	if record.InstallBaseline.Valid {
		v := record.InstallBaseline.Bool
		out.InstallBaseline = &v
	}
	if record.RegistrationStartedAt.Valid {
		t := record.RegistrationStartedAt.Time.UTC()
		out.StartedAt = &t
	}
	if record.RegistrationCompletedAt.Valid {
		t := record.RegistrationCompletedAt.Time.UTC()
		out.CompletedAt = &t
	}
	for _, r := range rows {
		step := Step{
			ID:           r.ID,
			StepName:     r.StepName,
			Label:        r.Label,
			Status:       r.Status,
			ProgressPct:  int(r.ProgressPct),
			Detail:       r.DetailJson,
			ErrorMessage: r.ErrorMessage,
			StepOrder:    int(r.StepOrder),
			CreatedAt:    r.CreatedAt.UTC(),
		}
		if r.StartedAt.Valid {
			t := r.StartedAt.Time.UTC()
			step.StartedAt = &t
		}
		if r.CompletedAt.Valid {
			t := r.CompletedAt.Time.UTC()
			step.CompletedAt = &t
		}
		out.Steps = append(out.Steps, step)
	}
	return out, nil
}
