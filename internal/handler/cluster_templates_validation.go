package handler

import (
	"encoding/json"
	"fmt"
	"strings"

	projectdomain "github.com/alphabravocompany/astronomer-go/internal/projects"
)

// CreateClusterTemplateRequest is the POST/PUT body shape.
// openapi:request CreateClusterTemplateRequest
type CreateClusterTemplateRequest struct {
	Name        string          `json:"name" validate:"required"`
	Description string          `json:"description"`
	Spec        json.RawMessage `json:"spec"`
}

// ApplyClusterTemplateRequest is the POST /clusters/{id}/template/ body.
// openapi:request ApplyClusterTemplateRequest
type ApplyClusterTemplateRequest struct {
	TemplateID string `json:"template_id" validate:"required"`
}

// ────────────────────────────────────────────────────────────────────────
// Spec validation
// ────────────────────────────────────────────────────────────────────────

// validTemplateTopKeys are the only top-level keys the spec is allowed
// to contain. Anything else is rejected at write time so a typo
// (e.g. "registration_polciy") doesn't silently fail to apply.
var validTemplateTopKeys = map[string]struct{}{
	"environment":         {},
	"labels":              {},
	"tools":               {},
	"default_project":     {},
	"registration_policy": {},
}

var validTemplateEnvironments = map[string]struct{}{
	"production":  {},
	"staging":     {},
	"development": {},
}

// validateTemplateSpec returns nil when the spec is a syntactically and
// enum-wise valid template body. It deliberately does NOT enforce that
// referenced tool slugs or chart presets actually exist — that's done at
// apply time by the worker so an operator can stage a template
// pre-catalog-sync without an order-of-operations footgun.
func validateTemplateSpec(raw json.RawMessage) error {
	if len(raw) == 0 {
		// Empty spec is a valid no-op template.
		return nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return fmt.Errorf("spec must be a JSON object: %w", err)
	}
	for k := range top {
		if _, ok := validTemplateTopKeys[k]; !ok {
			return fmt.Errorf("unknown spec key %q (allowed: environment, labels, tools, default_project, registration_policy)", k)
		}
	}
	if envRaw, ok := top["environment"]; ok {
		var env string
		if err := json.Unmarshal(envRaw, &env); err != nil {
			return fmt.Errorf("environment must be a string")
		}
		if _, ok := validTemplateEnvironments[env]; !ok {
			return fmt.Errorf("environment must be production|staging|development, got %q", env)
		}
	}
	if labelsRaw, ok := top["labels"]; ok {
		var labels map[string]string
		if err := json.Unmarshal(labelsRaw, &labels); err != nil {
			return fmt.Errorf("labels must be an object of string->string")
		}
	}
	if toolsRaw, ok := top["tools"]; ok {
		var tools []map[string]any
		if err := json.Unmarshal(toolsRaw, &tools); err != nil {
			return fmt.Errorf("tools must be an array of {slug, preset, values}")
		}
		for i, t := range tools {
			slug, _ := t["slug"].(string)
			if strings.TrimSpace(slug) == "" {
				return fmt.Errorf("tools[%d].slug is required", i)
			}
		}
	}
	if dpRaw, ok := top["default_project"]; ok {
		var dp map[string]any
		if err := json.Unmarshal(dpRaw, &dp); err != nil {
			return fmt.Errorf("default_project must be an object")
		}
		if name, _ := dp["name"].(string); strings.TrimSpace(name) == "" {
			return fmt.Errorf("default_project.name is required")
		}
		if pss, ok := dp["pod_security_profile"].(string); ok && pss != "" {
			if !projectdomain.IsValidPodSecurityProfile(pss) {
				return fmt.Errorf("default_project.pod_security_profile must be privileged|baseline|restricted")
			}
		}
	}
	if rpRaw, ok := top["registration_policy"]; ok {
		var rp map[string]any
		if err := json.Unmarshal(rpRaw, &rp); err != nil {
			return fmt.Errorf("registration_policy must be an object")
		}
		if days, ok := rp["token_rotation_days"]; ok {
			f, ok := days.(float64)
			if !ok || f < 0 {
				return fmt.Errorf("registration_policy.token_rotation_days must be a non-negative integer")
			}
		}
	}
	return nil
}
