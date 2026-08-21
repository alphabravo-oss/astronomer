package handler

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

var _ lokiAttachGate = (*MonitoringHandler)(nil)

// LokiAttachState reads hosted Loki metadata. Non-secret; used by attach GET/POST.
func (h *MonitoringHandler) LokiAttachState(ctx context.Context) lokiAttachState {
	st := lokiAttachState{Port: "443"}
	if h == nil || h.queries == nil {
		return st
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return st
	}
	meta := sharedStackMetadata(backend, "sharedLoki")
	st.Status = stringFromMap(meta, "status")
	st.IngestPublic = boolFromAny(meta["ingestPublic"])
	st.Host = strings.TrimSpace(stringFromMap(meta, "ingestHostname"))
	st.Mode = stringFromMap(meta, "mode")
	return st
}

// CheckLokiAttachCapacity is the sizer freeze/cap gate for a new attach.
// ok=false → HTTP 409 with ingest_cap_exceeded (or degraded handled by the caller).
func (h *MonitoringHandler) CheckLokiAttachCapacity(ctx context.Context, clusterID uuid.UUID) (string, string, bool) {
	state := h.LokiAttachState(ctx)
	snap := h.collectSizerSnapshot(ctx, "", "")
	includeNew := true
	if clusterID != uuid.Nil && !snap.input.ClustersUnreadable {
		if clusters, err := h.sizerListAllClusters(ctx); err == nil {
			now := time.Now()
			for _, c := range clusters {
				if c.ID != clusterID || c.IsLocal {
					continue
				}
				if c.LastHeartbeat.Valid && now.Sub(c.LastHeartbeat.Time) < sizerAgentConnectedFreshness {
					includeNew = false
				}
				break
			}
		}
	}
	return evaluateLokiAttachCapacity(state.Mode, snap.input, includeNew)
}

// evaluateLokiAttachCapacity returns 409 ingest_cap_exceeded when adding this
// cluster would fail the running mode's pass row, the sizer procedure would
// fail, or observed ingest is ≥ 80% of the global budget.
func evaluateLokiAttachCapacity(runningMode string, in sizerEvalInput, includeNewCluster bool) (code, msg string, ok bool) {
	if includeNewCluster {
		in.ConnectedClusters++
	}
	eval := evaluateSizer(in)
	if eval.Verdicts.Loki.Result != "pass" {
		msg := strings.Join(eval.Verdicts.Loki.Reasons, ", ")
		if msg == "" {
			msg = "loki sizer verdict failed"
		}
		return apierror.IngestCapExceeded, msg, false
	}
	mode := runningMode
	if mode == "" && eval.Verdicts.Loki.Mode != nil {
		mode = *eval.Verdicts.Loki.Mode
	}
	logBytes := eval.Estimates.LogBytesPerDay
	switch mode {
	case sizerModeSingleBinary:
		if in.ConnectedClusters > sizerSingleBinaryMaxClusters || logBytes > sizerSingleBinaryMaxLogBytes {
			return apierror.IngestCapExceeded, "running mode singleBinary cannot absorb another cluster", false
		}
	case sizerModeSimpleScalable:
		if in.ConnectedClusters > sizerSimpleScalableMaxClusters || logBytes > sizerSimpleScalableMaxLogBytes {
			return apierror.IngestCapExceeded, "running mode simpleScalable cannot absorb another cluster", false
		}
	}
	caps := sizerCapsForMode(sizerLokiVerdict{Result: "pass", Mode: stringPtr(mode)})
	observedMBps := float64(in.ObservedLogBytesPerDay) / 86400.0 / 1_000_000.0
	if observedMBps <= 0 {
		observedMBps = eval.Estimates.LogMBps
	}
	if caps.LokiGlobalBudgetMBPerSec > 0 && observedMBps >= 0.8*float64(caps.LokiGlobalBudgetMBPerSec) {
		return apierror.IngestCapExceeded, "observed ingest is at or above 80% of the global budget", false
	}
	return "", "", true
}
