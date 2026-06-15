package handler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	argocdclient "github.com/alphabravocompany/astronomer-go/internal/handler/argocd"
)

func validateArgoProjectSpec(spec argocdclient.AppProjectSpec) error {
	return validateArgoProjectSyncWindows(spec.SyncWindows)
}

func validateArgoProjectPatch(raw []byte) error {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	var patch struct {
		SyncWindows []argocdclient.AppProjectSyncWindow `json:"syncWindows"`
	}
	if err := json.Unmarshal(raw, &patch); err != nil {
		return fmt.Errorf("invalid project patch JSON")
	}
	return validateArgoProjectSyncWindows(patch.SyncWindows)
}

func validateArgoProjectSyncWindows(windows []argocdclient.AppProjectSyncWindow) error {
	for i, window := range windows {
		prefix := fmt.Sprintf("syncWindows[%d]", i)
		kind := strings.TrimSpace(window.Kind)
		if kind != "allow" && kind != "deny" {
			return fmt.Errorf("%s.kind must be allow or deny", prefix)
		}
		if !looksLikeCronSchedule(window.Schedule) {
			return fmt.Errorf("%s.schedule must be a cron expression", prefix)
		}
		duration, err := time.ParseDuration(strings.TrimSpace(window.Duration))
		if err != nil || duration <= 0 {
			return fmt.Errorf("%s.duration must be a positive duration such as 30m or 2h", prefix)
		}
		if !hasSyncWindowScope(window) {
			return fmt.Errorf("%s must include at least one application, namespace, or cluster selector", prefix)
		}
		if tz := strings.TrimSpace(window.TimeZone); tz != "" {
			if _, err := time.LoadLocation(tz); err != nil {
				return fmt.Errorf("%s.timeZone is not a valid IANA timezone", prefix)
			}
		}
	}
	return nil
}

func looksLikeCronSchedule(schedule string) bool {
	schedule = strings.TrimSpace(schedule)
	if schedule == "" {
		return false
	}
	if strings.HasPrefix(schedule, "@") {
		return true
	}
	fields := strings.Fields(schedule)
	return len(fields) == 5 || len(fields) == 6
}

func hasSyncWindowScope(window argocdclient.AppProjectSyncWindow) bool {
	return hasNonEmptyString(window.Applications) ||
		hasNonEmptyString(window.Namespaces) ||
		hasNonEmptyString(window.Clusters)
}

func hasNonEmptyString(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}
