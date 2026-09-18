package migrations_test

import (
	"strings"
	"testing"
)

func TestDurableJSONContractCorrections(t *testing.T) {
	initial := readMigration(t, "001_initial.up.sql")
	governance := readMigration(t, "049_durable_json_governance.up.sql")
	upgrade := readMigration(t, "052_durable_json_contract_corrections.up.sql")

	for name, contract := range map[string]string{
		"requirements initial default": "requirements jsonb DEFAULT '[]'::jsonb NOT NULL",
		"requirements override":        "('component_bundle_versions', 'requirements', 'array', 'additive')",
		"requirements preflight":       "jsonb_typeof(requirements) <> 'array'",
		"scan results initial default": "results jsonb DEFAULT '{}'::jsonb NOT NULL",
		"scan results override":        "('security_scan_results', 'results', 'object', 'additive')",
		"scan results preflight":       "jsonb_typeof(results) <> 'object'",
	} {
		var source string
		switch name {
		case "requirements initial default", "scan results initial default":
			source = initial
		case "requirements override", "scan results override":
			source = governance
		default:
			source = upgrade
		}
		if !strings.Contains(source, contract) {
			t.Fatalf("%s missing %q", name, contract)
		}
	}
}
