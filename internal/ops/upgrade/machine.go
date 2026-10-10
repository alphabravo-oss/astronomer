package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// Checkpoint is private durable state, separate from the retained v1 sanitized
// upgrade report. It contains identities and a plan binding, never credentials
// or private backup contents. A started mutation must be reconciled on resume.
type Checkpoint struct {
	SchemaVersion     int      `json:"schema_version"`
	Target            string   `json:"target"`
	PlanDigest        string   `json:"plan_digest"`
	Identity          Identity `json:"identity"`
	Completed         []Stage  `json:"completed"`
	Pending           Stage    `json:"pending,omitempty"`
	Applied           bool     `json:"applied"`
	Replaced          bool     `json:"replaced"`
	RollbackAttempted bool     `json:"rollback_attempted"`
}

type Store interface {
	Save(context.Context, Checkpoint) error
}

type Outcome struct {
	Applied  bool
	Replaced bool
}

// Driver is a typed adapter, not an arbitrary shell string. Verify retains the
// signer/chart/seven-image contract; Backup must establish a readable verified
// restore and exact schema range; DryRun retains server-side/PDB checks. Every
// mutating stage must fresh-check the identity and any disposable lease.
// Cleanup may touch only handles allocated by this run. Child-process ownership
// and finite deadlines must be enforced by the concrete adapter, including a
// driver that does not promptly return when its context is cancelled.
type Driver interface {
	Fence(context.Context, Identity, *DisposableLease) error
	Step(context.Context, Stage, Plan) (Outcome, error)
	// Reconcile observes a started apply/replacement after interruption; it must
	// not replay a potentially committed mutation or assume cancellation rolled it back.
	Reconcile(context.Context, Checkpoint) (Outcome, error)
}

type Result struct {
	ExitCode          int
	Result            string
	Mode              Mode
	FailedStage       Stage
	RollbackAttempted bool
	RollbackFailed    bool
	CleanupFailed     bool
}

type Machine struct {
	Driver Driver
	Store  Store
}

func (m Machine) Run(ctx context.Context, plan Plan, previous *Checkpoint) Result {
	result := Result{ExitCode: 1, Result: "failed", Mode: plan.Mode}
	if plan.Validate() != nil || m.Driver == nil || m.Store == nil {
		result.FailedStage = Verify
		return result
	}
	state := Checkpoint{SchemaVersion: 1, Target: plan.Target, PlanDigest: planDigest(plan), Identity: plan.Identity}
	if previous != nil {
		if validateCheckpoint(*previous, plan) != nil {
			result.FailedStage = Verify
			return result
		}
		state = *previous
		state.Completed = append([]Stage(nil), previous.Completed...)
		bounded, stop := context.WithTimeout(ctx, plan.Timeout)
		defer stop()
		if err := m.Driver.Fence(bounded, plan.Identity, plan.Replace); err != nil {
			result.FailedStage = Verify
			return result
		}
		_, err := m.Driver.Reconcile(bounded, state)
		if err == nil {
			err = bounded.Err()
		}
		if err != nil {
			result.Result = "outcome_unknown"
			result.FailedStage = state.Pending
			return result
		}
		// A checkpoint is an observation record, not fresh signer/backup proof.
		// Until an adapter can rebind those bytes, every resumed run stops for
		// assessment; no completed stage or potentially committed mutation replays.
		result.Result = "resume_assessment_required"
		result.FailedStage = state.Pending
		return result
	}
	steps := []Stage{Verify, Backup, DryRun}
	if plan.Mode == Execute {
		if plan.Replace != nil {
			steps = append(steps, ReplaceDatabase)
		}
		steps = append(steps, Apply, Observe)
	}
	for _, step := range steps {
		if completed(state, step) {
			continue
		}
		if err := m.perform(ctx, plan, step, &state); err != nil {
			result.FailedStage = step
			var child interface{ ExitCode() int }
			if errors.As(err, &child) && child.ExitCode() > 0 && child.ExitCode() < 256 {
				result.ExitCode = child.ExitCode()
			}
			if errors.Is(err, context.Canceled) {
				result.ExitCode = 130
			}
			if errors.Is(err, context.DeadlineExceeded) {
				result.ExitCode = 124
			}
			return m.finish(ctx, plan, &state, result)
		}
	}
	result.ExitCode, result.Result = 0, "passed"
	return m.finish(ctx, plan, &state, result)
}

func (m Machine) perform(ctx context.Context, plan Plan, stage Stage, state *Checkpoint) error {
	bounded, stop := context.WithTimeout(ctx, plan.Timeout)
	defer stop()
	if err := bounded.Err(); err != nil {
		return err
	}
	if err := m.Driver.Fence(bounded, plan.Identity, plan.Replace); err != nil {
		return err
	}
	state.Pending = stage
	if err := m.Store.Save(bounded, *state); err != nil {
		return err
	}
	outcome, err := m.Driver.Step(bounded, stage, plan)
	state.Applied, state.Replaced = state.Applied || outcome.Applied, state.Replaced || outcome.Replaced
	if err == nil {
		err = bounded.Err()
	}
	if err != nil {
		return err
	}
	state.Completed = append(state.Completed, stage)
	state.Pending = ""
	return m.Store.Save(bounded, *state)
}

func (m Machine) finish(ctx context.Context, plan Plan, state *Checkpoint, result Result) Result {
	// The caller may already be cancelled. Cleanup/rollback use fresh finite
	// contexts; neither inherits an expired timeout nor gets Background forever.
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), plan.CleanupTimeout)
	defer stop()
	if result.ExitCode != 0 && state.Applied && !state.RollbackAttempted {
		state.RollbackAttempted = true
		result.RollbackAttempted = true
		if m.Store.Save(cleanup, *state) != nil || m.Driver.Fence(cleanup, plan.Identity, plan.Replace) != nil {
			result.RollbackFailed = true
		} else {
			_, err := m.Driver.Step(cleanup, Rollback, plan)
			result.RollbackFailed = err != nil || cleanup.Err() != nil
		}
	}
	if m.Driver.Fence(cleanup, plan.Identity, plan.Replace) != nil {
		result.CleanupFailed = true
	} else {
		_, err := m.Driver.Step(cleanup, Cleanup, plan)
		result.CleanupFailed = err != nil || cleanup.Err() != nil
	}
	result.RollbackAttempted = result.RollbackAttempted || state.RollbackAttempted
	if result.CleanupFailed && result.ExitCode == 0 {
		result.ExitCode, result.Result = 1, "cleanup_failed"
	}
	return result
}

func completed(state Checkpoint, stage Stage) bool {
	for _, done := range state.Completed {
		if done == stage {
			return true
		}
	}
	return false
}

func validateCheckpoint(state Checkpoint, plan Plan) error {
	if state.SchemaVersion != 1 || state.Target != plan.Target || state.PlanDigest != planDigest(plan) || state.Identity != plan.Identity {
		return errors.New("checkpoint identity differs")
	}
	allowed := []Stage{Verify, Backup, DryRun}
	if plan.Mode == Execute {
		if plan.Replace != nil {
			allowed = append(allowed, ReplaceDatabase)
		}
		allowed = append(allowed, Apply, Observe)
	}
	if len(state.Completed) > len(allowed) {
		return errors.New("checkpoint stages exceed plan")
	}
	for i, stage := range state.Completed {
		if stage != allowed[i] {
			return errors.New("checkpoint stage order differs")
		}
	}
	if state.Pending != "" && (len(state.Completed) == len(allowed) || state.Pending != allowed[len(state.Completed)]) {
		return errors.New("checkpoint intent differs")
	}
	if state.Applied && !completed(state, Apply) && state.Pending != Apply {
		return errors.New("checkpoint applied state differs")
	}
	if state.Replaced && plan.Replace == nil {
		return errors.New("checkpoint replacement lacks lease")
	}
	return nil
}

func planDigest(plan Plan) string {
	payload, _ := json.Marshal(plan)
	hash := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(hash[:])
}
