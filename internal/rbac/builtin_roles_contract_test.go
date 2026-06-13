package rbac

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type seededRole struct {
	scope string
	name  string
	rules []Rule
}

func TestBuiltinRoleCatalogContract(t *testing.T) {
	roles := loadSeededRoles(t)

	required := []struct {
		scope string
		name  string
		check func(t *testing.T, role seededRole)
	}{
		{scope: "global", name: "Administrator", check: expectPermission("*", "*")},
		{scope: "global", name: "Read Only", check: expectPermission("*", "read")},
		{scope: "global", name: "User Administrator", check: expectPermission("users", "delete")},
		{scope: "global", name: "RBAC Administrator", check: expectPermission("rbac", "*")},
		{scope: "global", name: "Auditor", check: expectPermission("audit_logs", "read")},
		{scope: "global", name: "Platform Operator", check: expectPermission("agents", "update")},
		{scope: "global", name: "Security Auditor", check: expectPermission("security", "read")},
		{scope: "global", name: "Compliance Manager", check: expectPermission("security", "update")},
		{scope: "global", name: "GitOps Admin", check: expectPermission("argocd", "manage")},
		{scope: "global", name: "GitOps Viewer", check: expectPermission("argocd", "read")},
		{scope: "global", name: "Logging Viewer", check: expectPermission("logging", "read")},
		{scope: "global", name: "Monitoring Admin", check: expectPermission("monitoring", "delete")},
		{scope: "global", name: "Monitoring Viewer", check: expectPermission("monitoring", "read")},
		{scope: "global", name: "Restore Operator", check: expectPermission("backups", "manage")},
		{scope: "global", name: "Support Bundle Operator", check: expectPermission("support_bundles", "create")},
		{scope: "global", name: "Audit Viewer", check: expectPermission("audit_logs", "list")},
		{scope: "global", name: "Catalog Admin", check: expectPermission("cluster_templates", "delete")},
		{scope: "cluster", name: "Cluster Owner", check: expectPermission("*", "*")},
		{scope: "cluster", name: "Cluster Member", check: expectPermission("workloads", "restart")},
		{scope: "cluster", name: "Cluster Viewer", check: expectPermission("*", "watch")},
		{scope: "cluster", name: "Cluster Operator", check: expectPermission("argocd", "sync")},
		{scope: "cluster", name: "Cluster Troubleshooter", check: expectPermission("pods", "exec")},
		{scope: "cluster", name: "Catalog Installer", check: expectPermission("catalog", "create")},
		{scope: "cluster", name: "Cluster Backup Operator", check: expectPermission("backups", "delete")},
		{scope: "cluster", name: "Node Operator", check: expectPermission("nodes", "manage")},
		{scope: "cluster", name: "Service Mesh Operator", check: expectPermission("service_mesh", "update")},
		{scope: "cluster", name: "Storage Manager", check: expectPermission("storage", "delete")},
		{scope: "project", name: "Project Owner", check: expectPermission("*", "*")},
		{scope: "project", name: "Project Member", check: expectPermission("workloads", "scale")},
		{scope: "project", name: "Project Viewer", check: expectPermission("*", "watch")},
		{scope: "project", name: "Project Operator", check: expectPermission("argocd", "sync")},
		{scope: "project", name: "Project Troubleshooter", check: expectPermission("pods", "logs")},
		{scope: "project", name: "Config Manager", check: expectPermission("configmaps", "delete")},
		{scope: "project", name: "GitOps Deployer", check: expectPermission("argocd", "sync")},
		{scope: "project", name: "Namespace Operator", check: expectPermission("network_policies", "update")},
		{scope: "project", name: "Secret Manager", check: expectPermission("secrets", "read")},
		{scope: "project", name: "Service and Ingress Manager", check: expectPermission("services", "proxy")},
		{scope: "project", name: "Workload Deployer", check: expectPermission("workloads", "scale")},
		{scope: "project", name: "Workload Viewer", check: expectPermission("workloads", "read")},
	}

	for _, req := range required {
		role, ok := roles[req.scope+"::"+req.name]
		if !ok {
			t.Fatalf("missing builtin role %s/%s", req.scope, req.name)
		}
		req.check(t, role)
	}
}

func expectPermission(resource, verb string) func(*testing.T, seededRole) {
	return func(t *testing.T, role seededRole) {
		t.Helper()
		for _, rule := range role.rules {
			if rule.Resource != resource && rule.Resource != "*" {
				continue
			}
			for _, candidate := range rule.Verbs {
				if candidate == verb || candidate == "*" {
					return
				}
			}
		}
		t.Fatalf("role %s/%s missing %s:%s", role.scope, role.name, resource, verb)
	}
}

func loadSeededRoles(t *testing.T) map[string]seededRole {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve caller path")
	}
	migrationsDir := filepath.Join(filepath.Dir(file), "..", "db", "migrations")
	files := []string{
		filepath.Join(migrationsDir, "001_initial.up.sql"),
		filepath.Join(migrationsDir, "032_builtin_role_catalog.up.sql"),
		filepath.Join(migrationsDir, "098_rancher_grade_role_catalog.up.sql"),
	}

	roles := make(map[string]seededRole)
	scope := ""

	for _, path := range files {
		fh, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}

		scanner := bufio.NewScanner(fh)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			switch {
			case strings.HasPrefix(line, "INSERT INTO global_roles"):
				scope = "global"
			case strings.HasPrefix(line, "INSERT INTO cluster_roles"):
				scope = "cluster"
			case strings.HasPrefix(line, "INSERT INTO project_roles"):
				scope = "project"
			case strings.HasPrefix(line, "('"):
				role := parseSeedLine(t, scope, line)
				roles[scope+"::"+role.name] = role
			}
		}
		if err := scanner.Err(); err != nil {
			_ = fh.Close()
			t.Fatalf("scan %s: %v", path, err)
		}
		_ = fh.Close()
	}

	return roles
}

func parseSeedLine(t *testing.T, scope, line string) seededRole {
	t.Helper()

	trimmed := strings.TrimSuffix(strings.TrimSuffix(line, ","), ";")
	fields := sqlQuotedFields(trimmed)
	if len(fields) < 3 {
		t.Fatalf("unexpected seed line format: %s", line)
	}

	name := fields[0]
	rulesJSON := ""
	for _, field := range fields[1:] {
		if strings.HasPrefix(field, "[") {
			rulesJSON = field
			break
		}
	}
	if rulesJSON == "" {
		t.Fatalf("missing rules JSON in seed line: %s", line)
	}

	var rules []Rule
	if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil {
		t.Fatalf("parse rules for %s/%s: %v", scope, name, err)
	}

	return seededRole{
		scope: scope,
		name:  name,
		rules: rules,
	}
}

func sqlQuotedFields(line string) []string {
	fields := []string{}
	inQuote := false
	var current strings.Builder
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if ch != '\'' {
			if inQuote {
				current.WriteByte(ch)
			}
			continue
		}
		if inQuote && i+1 < len(line) && line[i+1] == '\'' {
			current.WriteByte('\'')
			i++
			continue
		}
		if inQuote {
			fields = append(fields, current.String())
			current.Reset()
			inQuote = false
			continue
		}
		inQuote = true
	}
	return fields
}
