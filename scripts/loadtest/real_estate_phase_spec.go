package main

import (
	"errors"
	"math"
	"slices"
)

const estateTiming = "headers-and-validated-body-histogram-v2"

type estatePhaseSpec struct {
	Name               string  `json:"name"`
	Mode               string  `json:"mode"`
	RPS                float64 `json:"rps"`
	WarmupSeconds      int     `json:"warmup_seconds"`
	MeasurementSeconds int     `json:"measurement_seconds"`
}
type estateSearchSpec struct {
	Namespace string   `json:"namespace"`
	Types     []string `json:"types"`
	Limit     int      `json:"limit"`
	Fanout    []string `json:"expected_active_cluster_ids"`
	TokenFile string   `json:"token_file,omitempty"`
}

func (m estateManifest) validatePhases() error {
	if len(m.Phases) < 1 || len(m.Phases) > 8 {
		return errors.New("declare 1–8 measured phases")
	}
	seen := map[string]bool{}
	search := false
	for _, p := range m.Phases {
		if !estateName.MatchString(p.Name) || seen[p.Name] || !slices.Contains([]string{"idle", "resources", "delivery", "search"}, p.Mode) {
			return errors.New("invalid or duplicate phase")
		}
		seen[p.Name] = true
		if p.WarmupSeconds < 300 || p.WarmupSeconds > 3600 || p.MeasurementSeconds < 1800 || p.MeasurementSeconds > 14400 {
			return errors.New("each phase needs 5–60m warmup and 30m–4h measurement")
		}
		if (p.Mode == "idle" && p.RPS != 0) || (p.Mode != "idle" && (p.RPS < 0.01 || p.RPS > 1000 || math.IsNaN(p.RPS) || math.IsInf(p.RPS, 0))) {
			return errors.New("invalid phase rate")
		}
		// Search has its own per-user limiter; do not produce an implicit flood.
		if p.Mode == "delivery" && p.RPS*float64(p.MeasurementSeconds) < float64(len(m.Members)*(m.Tier+2)) {
			return errors.New("delivery rate/window cannot cover all assignments")
		}
		if p.Mode == "resources" && p.RPS*float64(p.MeasurementSeconds) < float64(len(m.Members)*3) {
			return errors.New("resource rate/window cannot cover all members")
		}
		if p.Mode == "search" {
			search = true
			if p.RPS > 1.0/6 {
				return errors.New("search phase exceeds 10/minute sustained limit")
			}
		}
	}
	if search != (m.Search != nil) {
		return errors.New("search specification must match declared search phase")
	}
	if !search {
		return nil
	}
	s := m.Search
	if !estateName.MatchString(s.Namespace) || len(s.Types) < 1 || len(s.Types) > 3 || s.Limit < 1 || s.Limit > 1000 || len(s.Fanout) < 1 || len(s.Fanout) > 32 {
		return errors.New("invalid bounded search scope")
	}
	seen = map[string]bool{}
	for _, kind := range s.Types {
		if !slices.Contains([]string{"pods", "deployments", "services"}, kind) || seen[kind] {
			return errors.New("invalid search type")
		}
		seen[kind] = true
	}
	seen = map[string]bool{}
	for _, id := range s.Fanout {
		if !validEstateUUID(id) || seen[id] {
			return errors.New("invalid search fanout")
		}
		seen[id] = true
	}
	return nil
}
