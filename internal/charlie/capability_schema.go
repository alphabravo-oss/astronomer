package charlie

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

type CapabilityFieldSchema struct {
	Type        string
	Required    bool
	Minimum     int64
	Maximum     int64
	MaxLength   int
	Pattern     string
	Enum        []string
	Description string
}

var (
	uuidPattern       = `^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
	opaqueIDPattern   = `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`
	componentPattern  = `^[a-z0-9][a-z0-9-]{0,62}$`
	workloadPattern   = `^(deployment|statefulset)/[a-z0-9][a-z0-9-]{0,62}$`
	durationPattern   = `^[1-9][0-9]*[smhd]$`
	digestPattern     = `^sha256:[0-9a-f]{64}$`
	allowedQueryNames = []string{"availability", "latency", "errors", "saturation"}
)

func capabilityFieldSchemas(name string) map[string]CapabilityFieldSchema {
	fields := map[string]CapabilityFieldSchema{}
	stringField := func(field string, required bool, pattern string) {
		fields[field] = CapabilityFieldSchema{Type: "string", Required: required, MaxLength: 128, Pattern: pattern}
	}
	integerField := func(field string, required bool, min, max int64) {
		fields[field] = CapabilityFieldSchema{Type: "integer", Required: required, Minimum: min, Maximum: max}
	}
	switch name {
	case "astronomer.installation.configuration":
		fields["keys"] = CapabilityFieldSchema{Type: "array", MaxLength: 32, Enum: safeConfigurationKeys()}
	case "astronomer.management.workloads":
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.management.workload_get", "astronomer.management.rollout_status":
		stringField("workload", true, workloadPattern)
	case "astronomer.management.pods":
		stringField("component", false, componentPattern)
		fields["phase"] = CapabilityFieldSchema{Type: "string", Enum: []string{"Pending", "Running", "Succeeded", "Failed", "Unknown"}, MaxLength: 16}
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.management.resource_usage":
		stringField("component", false, componentPattern)
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.management.jobs":
		fields["status"] = CapabilityFieldSchema{Type: "string", Enum: []string{"active", "succeeded", "failed", "suspended"}, MaxLength: 16}
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.management.job_get":
		stringField("job", true, `^(job|cronjob)/[a-z0-9][a-z0-9-]{0,62}$`)
	case "astronomer.management.events":
		stringField("component", false, componentPattern)
		stringField("since", false, durationPattern)
		integerField("limit", false, 1, 200)
	case "astronomer.management.pod_logs":
		stringField("pod", true, componentPattern)
		stringField("container", true, componentPattern)
		integerField("lines", false, 1, 200)
	case "astronomer.queue.failed_tasks":
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
		stringField("task_type", false, opaqueIDPattern)
	case "astronomer.queue.tasks":
		fields["state"] = CapabilityFieldSchema{Type: "string", Enum: []string{"pending", "active", "scheduled", "retry", "archived"}, MaxLength: 16}
		fields["queue"] = CapabilityFieldSchema{Type: "string", Enum: append([]string(nil), charlieQueueNames...), MaxLength: 16}
		stringField("task_type", false, opaqueIDPattern)
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.queue.task_get":
		stringField("task_id", true, opaqueIDPattern)
	case "astronomer.task_outbox.list":
		fields["status"] = CapabilityFieldSchema{Type: "string", Enum: []string{"pending", "delivering", "failed", "delivered", "dead"}, MaxLength: 16}
		stringField("task_type", false, opaqueIDPattern)
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.task_outbox.get":
		stringField("outbox_id", true, uuidPattern)
	case "astronomer.task_outbox.retry_delivery":
		stringField("resource_id", true, opaqueIDPattern)
		stringField("outbox_id", true, uuidPattern)
		stringField("operation_id", true, opaqueIDPattern)
		annotateWriteCorrelators(fields)
	case "astronomer.controllers.alerts":
		fields["status"] = CapabilityFieldSchema{Type: "string", Enum: []string{"active", "acknowledged", "resolved"}, MaxLength: 16}
		stringField("controller", false, componentPattern)
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.catalog.operations", "astronomer.tools.operations",
		"astronomer.monitoring.operations", "astronomer.logging.operations", "astronomer.workloads.operations":
		fields["status"] = CapabilityFieldSchema{Type: "string", Enum: []string{"pending", "running", "completed", "failed", "superseded", "cancelled"}, MaxLength: 16}
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.catalog.operation_get", "astronomer.tools.operation_get",
		"astronomer.monitoring.operation_get", "astronomer.logging.operation_get", "astronomer.workloads.operation_get":
		stringField("record_id", true, uuidPattern)
	case "astronomer.observability.health":
		fields["query_template"] = CapabilityFieldSchema{Type: "string", Required: true, Enum: allowedQueryNames, MaxLength: 32}
		fields["range"] = CapabilityFieldSchema{Type: "string", Enum: []string{"5m", "15m", "1h", "6h"}, MaxLength: 3}
	case "astronomer.alert.list":
		fields["status"] = CapabilityFieldSchema{Type: "string", Enum: []string{"active", "acknowledged", "resolved"}, MaxLength: 16}
		fields["severity"] = CapabilityFieldSchema{Type: "string", Enum: []string{"info", "warning", "critical"}, MaxLength: 16}
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.alert.get":
		stringField("alert_id", true, uuidPattern)
	case "astronomer.audit.recent_changes":
		fields["resource_type"] = CapabilityFieldSchema{Type: "string", Enum: []string{"platform_setting", "management_workload", "backup", "cluster_deployment", "charlie_connection"}, MaxLength: 32}
		stringField("resource_id", false, opaqueIDPattern)
		stringField("since", false, durationPattern)
		integerField("limit", false, 1, 100)
	case "astronomer.audit.search":
		stringField("resource_type", false, opaqueIDPattern)
		stringField("resource_id", false, opaqueIDPattern)
		stringField("action", false, opaqueIDPattern)
		stringField("action_class", false, opaqueIDPattern)
		fields["result"] = CapabilityFieldSchema{Type: "string", Enum: []string{"success", "failure", "error"}, MaxLength: 16}
		stringField("source", false, opaqueIDPattern)
		stringField("correlation_id", false, opaqueIDPattern)
		stringField("since", false, durationPattern)
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.catalog.repositories":
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.delivery.overview":
		stringField("project_id", true, uuidPattern)
	case "astronomer.delivery.sources":
		stringField("project_id", true, uuidPattern)
		fields["status"] = CapabilityFieldSchema{Type: "string", Enum: []string{"pending", "ready", "failed", "revoked"}, MaxLength: 16}
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.delivery.source_get":
		stringField("project_id", true, uuidPattern)
		stringField("source_id", true, uuidPattern)
	case "astronomer.delivery.bundles", "astronomer.delivery.targets":
		stringField("project_id", true, uuidPattern)
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.delivery.bundle_get":
		stringField("project_id", true, uuidPattern)
		stringField("bundle_id", true, uuidPattern)
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.delivery.target_preview":
		stringField("project_id", true, uuidPattern)
		stringField("target_id", true, uuidPattern)
	case "astronomer.delivery.rollouts":
		stringField("project_id", true, uuidPattern)
		fields["state"] = CapabilityFieldSchema{Type: "string", Enum: []string{"resolving", "awaiting_approval", "queued", "progressing", "paused", "aborted", "rejected", "succeeded", "failed", "rolling_back", "rolled_back", "rollback_failed"}, MaxLength: 32}
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.delivery.rollout_get":
		stringField("project_id", true, uuidPattern)
		stringField("rollout_id", true, uuidPattern)
	case "astronomer.delivery.deployments":
		stringField("project_id", true, uuidPattern)
		stringField("cluster_id", false, uuidPattern)
		fields["phase"] = CapabilityFieldSchema{Type: "string", Enum: []string{"pending", "blocked", "applying", "ready", "degraded", "failed", "suspended", "deleting", "removed", "unknown"}, MaxLength: 16}
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.delivery.deployment_get":
		stringField("project_id", true, uuidPattern)
		stringField("deployment_id", true, uuidPattern)
	case "astronomer.cluster_agents.summary":
		integerField("stale_after_seconds", false, 30, 86400)
	case "astronomer.cluster_agents.list":
		stringField("environment", false, componentPattern)
		stringField("region", false, componentPattern)
		fields["state"] = CapabilityFieldSchema{Type: "string", Enum: []string{"connected", "disconnected", "never"}, MaxLength: 16}
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.cluster_agents.get":
		stringField("cluster_id", true, uuidPattern)
	case "astronomer.cluster_agents.connection_history":
		stringField("cluster_id", true, uuidPattern)
		stringField("since", false, durationPattern)
		integerField("limit", false, 1, 200)
	case "astronomer.tunnel.recent_errors":
		stringField("since", false, durationPattern)
		integerField("limit", false, 1, 200)
		stringField("connection_id", false, opaqueIDPattern)
	case "astronomer.management.workload_restart", "astronomer.management.workload_rollout":
		stringField("resource_id", true, opaqueIDPattern)
		stringField("workload", true, workloadPattern)
		stringField("operation_id", true, opaqueIDPattern)
		annotateWriteCorrelators(fields)
	case "astronomer.management.workload_scale":
		stringField("resource_id", true, opaqueIDPattern)
		stringField("workload", true, workloadPattern)
		integerField("replicas", true, 2, 20)
		stringField("operation_id", true, opaqueIDPattern)
		annotateWriteCorrelators(fields)
		replicas := fields["replicas"]
		replicas.Description = "Desired replica count in [2,20] for the mutable management Deployment."
		fields["replicas"] = replicas
		workload := fields["workload"]
		workload.Description = "Target as deployment/<name>, e.g. deployment/astronomer-worker."
		fields["workload"] = workload
	case "astronomer.queue.retry_task":
		stringField("resource_id", true, opaqueIDPattern)
		stringField("task_id", true, opaqueIDPattern)
		stringField("operation_id", true, opaqueIDPattern)
		annotateWriteCorrelators(fields)
	case "astronomer.management.run_job":
		stringField("resource_id", true, opaqueIDPattern)
		fields["job"] = CapabilityFieldSchema{Type: "string", Required: true, Enum: []string{"management-plane-backup", "restore-drill"}, MaxLength: 32}
		stringField("operation_id", true, opaqueIDPattern)
		annotateWriteCorrelators(fields)
	case "astronomer.tunnel.restart_component":
		stringField("resource_id", true, opaqueIDPattern)
		fields["component"] = CapabilityFieldSchema{Type: "string", Required: true, Enum: []string{"server", "worker"}, MaxLength: 16}
		stringField("operation_id", true, opaqueIDPattern)
		annotateWriteCorrelators(fields)
	case "astronomer.delivery.rollout_pause", "astronomer.delivery.rollout_resume",
		"astronomer.delivery.rollout_retry_failed", "astronomer.delivery.rollout_rollback":
		deliveryWriteFields(fields, stringField, integerField, "rollout_id", "expected_fence")
		stringField("reason_code", false, opaqueIDPattern)
	case "astronomer.delivery.rollout_approve":
		deliveryWriteFields(fields, stringField, integerField, "rollout_id", "expected_fence")
		integerField("cohort", true, -1, 10000)
		stringField("binding_digest", true, digestPattern)
		integerField("expires_in_seconds", true, 60, 86400)
	case "astronomer.delivery.deployment_reconcile":
		deliveryWriteFields(fields, stringField, integerField, "deployment_id", "expected_generation")
		stringField("reason_code", false, opaqueIDPattern)
	}
	return fields
}

func deliveryWriteFields(fields map[string]CapabilityFieldSchema, stringField func(string, bool, string), integerField func(string, bool, int64, int64), idField, preconditionField string) {
	stringField("resource_id", true, opaqueIDPattern)
	stringField("operation_id", true, opaqueIDPattern)
	stringField("project_id", true, uuidPattern)
	stringField(idField, true, uuidPattern)
	minimum := int64(1)
	if preconditionField == "expected_generation" {
		minimum = 0
	}
	integerField(preconditionField, true, minimum, 1<<62)
	annotateWriteCorrelators(fields)
}

func validateCapabilityArguments(capability CapabilityDescriptor, arguments map[string]json.RawMessage) error {
	fields := capabilityFieldSchemas(capability.Name)
	if len(fields) != len(capability.AcceptedFields) {
		return fmt.Errorf("capability schema is incomplete")
	}
	for name, schema := range fields {
		raw, present := arguments[name]
		if !present {
			if schema.Required {
				return fmt.Errorf("required field is missing")
			}
			continue
		}
		switch schema.Type {
		case "string":
			var value string
			if json.Unmarshal(raw, &value) != nil || value == "" || len(value) > schema.MaxLength {
				return fmt.Errorf("string field is invalid")
			}
			if schema.Pattern != "" && !regexp.MustCompile(schema.Pattern).MatchString(value) {
				return fmt.Errorf("string field format is invalid")
			}
			if len(schema.Enum) > 0 && !containsString(schema.Enum, value) {
				return fmt.Errorf("string field value is not allowed")
			}
		case "integer":
			var value int64
			if json.Unmarshal(raw, &value) != nil || value < schema.Minimum || value > schema.Maximum {
				return fmt.Errorf("integer field is out of bounds")
			}
		case "array":
			var values []string
			if json.Unmarshal(raw, &values) != nil || len(values) > schema.MaxLength {
				return fmt.Errorf("array field is invalid")
			}
			for _, value := range values {
				if !containsString(schema.Enum, value) {
					return fmt.Errorf("array field value is not allowed")
				}
			}
		default:
			return fmt.Errorf("field schema type is unsupported")
		}
	}
	return nil
}

// annotateWriteCorrelators documents session-scoped resource_id and client
// operation_id so the model fills them without asking operators for tool names
// or opaque product IDs.
func annotateWriteCorrelators(fields map[string]CapabilityFieldSchema) {
	if field, ok := fields["resource_id"]; ok {
		field.Description = "Session-scoped resource id from product context resource_ids. Default install-wide scope is 'local'."
		fields["resource_id"] = field
	}
	if field, ok := fields["operation_id"]; ok {
		field.Description = "Any fresh opaque correlator (e.g. a UUID you generate). Product replaces this with the trusted action id before the adapter runs."
		fields["operation_id"] = field
	}
}

func capabilityJSONSchema(capability CapabilityDescriptor) map[string]any {
	properties := map[string]any{}
	required := []string{}
	for name, field := range capabilityFieldSchemas(capability.Name) {
		property := map[string]any{"type": field.Type}
		if field.Type == "integer" {
			property["minimum"], property["maximum"] = field.Minimum, field.Maximum
		}
		if field.Type == "string" && field.MaxLength > 0 {
			property["maxLength"] = field.MaxLength
		}
		if field.Pattern != "" {
			property["pattern"] = field.Pattern
		}
		if len(field.Enum) > 0 {
			if field.Type == "array" {
				property["items"] = map[string]any{"type": "string", "enum": field.Enum}
				property["maxItems"] = field.MaxLength
			} else {
				property["enum"] = field.Enum
			}
		}
		if field.Description != "" {
			property["description"] = field.Description
		}
		properties[name] = property
		if field.Required {
			required = append(required, name)
		}
	}
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		sort.Strings(required)
		schema["required"] = required
	}
	return schema
}

func safeConfigurationKeys() []string {
	return []string{"feature.charlie", "feature.alerting", "feature.backups", "feature.monitoring", "session_timeout_minutes", "audit_log_retention_months"}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
