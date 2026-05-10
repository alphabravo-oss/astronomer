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
		{scope: "cluster", name: "Cluster Owner", check: expectPermission("*", "*")},
		{scope: "cluster", name: "Cluster Member", check: expectPermission("workloads", "restart")},
		{scope: "cluster", name: "Cluster Viewer", check: expectPermission("*", "watch")},
		{scope: "cluster", name: "Cluster Operator", check: expectPermission("argocd", "sync")},
		{scope: "cluster", name: "Cluster Troubleshooter", check: expectPermission("pods", "exec")},
		{scope: "project", name: "Project Owner", check: expectPermission("*", "*")},
		{scope: "project", name: "Project Member", check: expectPermission("workloads", "scale")},
		{scope: "project", name: "Project Viewer", check: expectPermission("*", "watch")},
		{scope: "project", name: "Project Operator", check: expectPermission("argocd", "sync")},
		{scope: "project", name: "Project Troubleshooter", check: expectPermission("pods", "logs")},
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
	parts := strings.SplitN(trimmed, "', '", 3)
	if len(parts) != 3 {
		t.Fatalf("unexpected seed line format: %s", line)
	}

	name := strings.TrimPrefix(parts[0], "('")
	rulesJSON := strings.TrimSuffix(parts[2], "', true)")

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
