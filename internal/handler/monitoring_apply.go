package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *MonitoringHandler) applyMonitoringStack(ctx context.Context, clusterID string, msgType protocol.MessageType, req MonitoringStackRequest, values map[string]any) (*protocol.HelmResultPayload, error) {
	if h.helm == nil {
		return nil, fmt.Errorf("helm requester not configured")
	}
	if req.StorageConfigID != "" {
		// nil authorizer, deliberately: this runs in the operation executor,
		// which has no HTTP caller and no bindings to check. The reference was
		// authorized when the operation was ENQUEUED — monitoringStackPayload
		// runs clusterStorageConfigAuthorizer against the same
		// req.StorageConfigID before enqueueClusterStackOperation persists it
		// into the envelope this re-reads. Adding a check here would evaluate
		// an empty binding set and fail every install.
		secretSpec, err := h.objectStoreSecretSpec(ctx, req.StorageConfigID, req.ObjectStorageSecretName, req.ReleaseName+"-thanos-objstore", nil)
		if err != nil {
			return nil, err
		}
		if err := h.ensureObjectStoreSecret(ctx, clusterID, req.Namespace, secretSpec); err != nil {
			return nil, err
		}
	}
	return h.helm.Do(ctx, clusterID, msgType, protocol.HelmRequestPayload{
		ReleaseName: req.ReleaseName,
		Namespace:   req.Namespace,
		ChartName:   "kube-prometheus-stack",
		RepoURL:     "https://prometheus-community.github.io/helm-charts",
		Version:     req.ChartVersion,
		Values:      values,
		Timeout:     900,
	})
}

func scrapeIntervalSeconds(raw string) int32 {
	if raw == "" {
		return 30
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 30
	}
	return int32(d.Seconds())
}

func defaultInt32(v, fallback int32) int32 {
	if v <= 0 {
		return fallback
	}
	return v
}

func nullableNow(ok bool) pgtype.Timestamptz {
	if !ok {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
}

func specHash(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func mapFromMapValue(v any) map[string]any {
	out, _ := v.(map[string]any)
	if out == nil {
		return map[string]any{}
	}
	return out
}

func (h *MonitoringHandler) observeRelease(ctx context.Context, clusterID string, ref releaseRef) (map[string]any, bool, []string) {
	if h.helm == nil || clusterID == "" || ref.ReleaseName == "" || ref.Namespace == "" {
		return nil, false, nil
	}
	result, err := h.helm.Status(ctx, clusterID, ref.ReleaseName, ref.Namespace)
	observed := map[string]any{
		"clusterId":   clusterID,
		"namespace":   ref.Namespace,
		"releaseName": ref.ReleaseName,
		"observedAt":  time.Now().UTC().Format(time.RFC3339),
	}
	if err != nil {
		observed["status"] = "missing"
		observed["error"] = err.Error()
		return observed, true, []string{"helm release not found or not healthy"}
	}
	observed["status"] = result.Status
	observed["revision"] = result.Revision
	return observed, false, nil
}

func boolPtrValue(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func nullablePgTime(ts pgtype.Timestamptz) any {
	if !ts.Valid {
		return nil
	}
	return ts.Time.UTC().Format(time.RFC3339)
}
