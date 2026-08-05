package charlie

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

type CapabilityFieldSchema struct {
	Type      string
	Required  bool
	Minimum   int64
	Maximum   int64
	MaxLength int
	Pattern   string
	Enum      []string
}

var (
	uuidPattern       = `^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
	opaqueIDPattern   = `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`
	componentPattern  = `^[a-z0-9][a-z0-9-]{0,62}$`
	workloadPattern   = `^(deployment|statefulset)/[a-z0-9][a-z0-9-]{0,62}$`
	durationPattern   = `^[1-9][0-9]*[smhd]$`
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
	case "astronomer.management.workload_get":
		stringField("workload", true, workloadPattern)
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
		fields["resource_type"] = CapabilityFieldSchema{Type: "string", Enum: []string{"platform_setting", "management_workload", "backup", "argocd_application", "charlie_connection"}, MaxLength: 32}
		stringField("resource_id", false, opaqueIDPattern)
		stringField("since", false, durationPattern)
		integerField("limit", false, 1, 100)
	case "astronomer.agent_fleet.summary":
		integerField("stale_after_seconds", false, 30, 86400)
	case "astronomer.agent_fleet.list":
		stringField("environment", false, componentPattern)
		stringField("region", false, componentPattern)
		fields["state"] = CapabilityFieldSchema{Type: "string", Enum: []string{"connected", "disconnected", "never"}, MaxLength: 16}
		integerField("page", false, 1, 10000)
		integerField("page_size", false, 1, 100)
	case "astronomer.agent_fleet.get", "astronomer.agent_fleet.upgrade_status", "astronomer.agent_fleet.ingestion_health":
		stringField("cluster_id", true, uuidPattern)
	case "astronomer.agent_fleet.connection_history":
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
	case "astronomer.management.workload_scale":
		stringField("resource_id", true, opaqueIDPattern)
		stringField("workload", true, workloadPattern)
		integerField("replicas", true, 2, 20)
		stringField("operation_id", true, opaqueIDPattern)
	case "astronomer.argocd.self_management_sync":
		stringField("resource_id", true, opaqueIDPattern)
		stringField("application", true, componentPattern)
		stringField("operation_id", true, opaqueIDPattern)
	case "astronomer.queue.retry_task":
		stringField("resource_id", true, opaqueIDPattern)
		stringField("task_id", true, opaqueIDPattern)
		stringField("operation_id", true, opaqueIDPattern)
	case "astronomer.management.run_job":
		stringField("resource_id", true, opaqueIDPattern)
		fields["job"] = CapabilityFieldSchema{Type: "string", Required: true, Enum: []string{"management-plane-backup", "restore-drill"}, MaxLength: 32}
		stringField("operation_id", true, opaqueIDPattern)
	case "astronomer.tunnel.restart_component":
		stringField("resource_id", true, opaqueIDPattern)
		fields["component"] = CapabilityFieldSchema{Type: "string", Required: true, Enum: []string{"server", "worker"}, MaxLength: 16}
		stringField("operation_id", true, opaqueIDPattern)
	}
	return fields
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
