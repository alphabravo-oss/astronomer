package delivery

import (
	"context"
	"time"
)

// Assignment notifications schedule status only. The single runtime owner also
// services these channels while awaiting a desired-state response; mutation
// passes remain serialized with checkpoint updates.
func (r *Runtime) assignmentWake() <-chan struct{} {
	if r.config.AssignmentObservations == nil {
		return nil
	}
	return r.config.AssignmentObservations.Wake()
}
func (r *Runtime) assignmentStatusDue() <-chan time.Time {
	if r.assignmentStatusTimer == nil {
		return nil
	}
	return r.assignmentStatusTimer.C
}
func (r *Runtime) noticeAssignmentChanges() {
	if r.config.AssignmentObservations == nil {
		return
	}
	dirty := r.config.AssignmentObservations.ConsumeDirty()
	if len(dirty) == 0 || !r.config.Connected() || r.config.ObservationFreshness == nil || !r.config.ObservationFreshness() {
		return
	}
	if r.assignmentStatusTimer != nil {
		return
	}
	// Cap event rate while keeping default connected detection below 30 seconds.
	// A non-resetting debounce prevents a continuous event stream postponing work.
	interval := min(r.config.StatusInterval, 15*time.Second)
	delay := 250 * time.Millisecond
	if remaining := interval - r.now().Sub(r.lastStatusAttempt); remaining > delay {
		delay = remaining
	}
	r.assignmentStatusTimer = time.NewTimer(delay)
}
func (r *Runtime) stopAssignmentStatusTimer() {
	if r.assignmentStatusTimer != nil {
		r.assignmentStatusTimer.Stop()
		r.assignmentStatusTimer = nil
	}
}
func (r *Runtime) attemptScheduledStatus(ctx context.Context, send Sender) {
	r.stopAssignmentStatusTimer()
	if !r.config.Connected() {
		return
	}
	r.lastStatusAttempt = r.now()
	if err := r.sendStatus(ctx, send); err != nil && ctx.Err() == nil {
		r.config.Logger.Warn("delivery status send failed", "error_code", stableRuntimeError(err))
	}
}
