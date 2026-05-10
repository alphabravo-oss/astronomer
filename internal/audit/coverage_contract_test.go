package audit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

func TestKeyMutatingHandlersEmitAudit(t *testing.T) {
	expected := map[string][]string{
		"../handler/auth.go": {
			"Login",
			"Refresh",
			"Logout",
			"ChangePassword",
			"CreateToken",
			"RevokeToken",
		},
		"../handler/argocd.go": {
			"CreateInstance",
			"DeleteInstance",
			"UpdateInstance",
			"SyncApp",
			"RefreshApp",
			"CreateApplication",
			"PatchApplication",
			"DeleteApplication",
			"CreateProject",
			"PatchProject",
			"DeleteProject",
			"CreateApplicationSet",
			"DeleteApplicationSet",
			"RegisterManagedCluster",
			"UnregisterManagedCluster",
			"CreateRepo",
			"DeleteRepo",
		},
		"../handler/backups.go": {
			"CreateStorageConfig",
			"DeleteStorageConfig",
			"UpdateStorageConfig",
			"CreateBackup",
			"DeleteBackup",
			"CreateSchedule",
			"DeleteSchedule",
			"UpdateSchedule",
			"TriggerSchedule",
			"CreateRestore",
		},
		"../handler/clusters.go": {
			"Create",
			"Update",
			"Delete",
			"UpdateRegistryConfig",
		},
		"../handler/dex_config.go": {
			"CreateConnector",
			"UpdateConnector",
			"DeleteConnector",
			"UpdateSettings",
			"Apply",
			"RegisterAsSSO",
		},
		"../handler/projects.go": {
			"Create",
			"Update",
			"Delete",
			"AddNamespace",
			"RemoveNamespace",
		},
		"../handler/rbac.go": {
			"CreateGlobalRole",
			"UpdateGlobalRole",
			"DeleteGlobalRole",
			"CreateClusterRole",
			"UpdateClusterRole",
			"DeleteClusterRole",
			"CreateProjectRole",
			"UpdateProjectRole",
			"DeleteProjectRole",
			"CreateGlobalRoleBinding",
			"DeleteGlobalRoleBinding",
			"CreateClusterRoleBinding",
			"DeleteClusterRoleBinding",
			"CreateProjectRoleBinding",
			"DeleteProjectRoleBinding",
		},
		"../handler/security.go": {
			"CreateTemplate",
			"DeleteTemplate",
			"UpdateTemplate",
			"CreatePolicy",
			"ApplyPolicy",
			"DeletePolicy",
			"CreateScan",
		},
		"../handler/catalog.go": {
			"CreateRepo",
			"UpdateRepo",
			"DeleteRepo",
			"SyncRepo",
			"CreateInstallation",
			"DeleteInstallation",
			"UpgradeInstalledChart",
			"RollbackInstalledChart",
		},
		"../handler/logging.go": {
			"CreateOutput",
			"UpdateOutput",
			"DeleteOutput",
			"CreatePipeline",
			"UpdatePipeline",
			"DeletePipeline",
		},
		"../handler/monitoring.go": {
			"UpdateBackendConfig",
			"UpdateEndpoint",
			"InstallSharedThanosStack",
			"UpgradeSharedThanosStack",
			"ReplaceSharedThanosStack",
			"UninstallSharedThanosStack",
			"InstallSharedAlertmanager",
			"UpgradeSharedAlertmanager",
			"ReplaceSharedAlertmanager",
			"UninstallSharedAlertmanager",
			"UpdateClusterConfig",
			"InstallStack",
			"UpgradeStack",
			"ReplaceStack",
			"UninstallStack",
		},
		"../handler/users.go": {
			"CreateUser",
			"UpdateUser",
			"DeleteUser",
		},
		"../handler/alerting.go": {
			"CreateChannel",
			"UpdateChannel",
			"DeleteChannel",
			"CreateRule",
			"UpdateRule",
			"DeleteRule",
			"EnableRule",
			"DisableRule",
			"AcknowledgeEvent",
			"ResolveEvent",
			"CreateSilence",
			"ExpireSilence",
			"DeleteSilence",
		},
		"../handler/tools.go": {
			"Install",
			"Upgrade",
			"Uninstall",
			"Adopt",
		},
	}

	allowedAuditCalls := map[string]struct{}{
		"recordAudit":        {},
		"recordAuditAs":      {},
		"recordProjectAudit": {},
	}

	fset := token.NewFileSet()
	for path, wantFuncs := range expected {
		fullPath, err := filepath.Abs(path)
		if err != nil {
			t.Fatalf("abs path %s: %v", path, err)
		}
		file, err := parser.ParseFile(fset, fullPath, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", fullPath, err)
		}

		funcs := map[string]*ast.FuncDecl{}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			funcs[fn.Name.Name] = fn
		}

		found := map[string]bool{}
		for name, fn := range funcs {
			if functionContainsAuditCall(fn, funcs, allowedAuditCalls, map[string]bool{}) {
				found[name] = true
			}
		}

		for _, want := range wantFuncs {
			if !found[want] {
				t.Errorf("%s: expected handler %s to emit audit", path, want)
			}
		}
	}
}

func functionContainsAuditCall(fn *ast.FuncDecl, funcs map[string]*ast.FuncDecl, allowed map[string]struct{}, visiting map[string]bool) bool {
	if fn == nil || fn.Body == nil {
		return false
	}
	if visiting[fn.Name.Name] {
		return false
	}
	visiting[fn.Name.Name] = true
	defer delete(visiting, fn.Name.Name)

	hasAudit := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if _, ok := allowed[fun.Name]; ok {
				hasAudit = true
				return false
			}
			if target, ok := funcs[fun.Name]; ok && functionContainsAuditCall(target, funcs, allowed, visiting) {
				hasAudit = true
				return false
			}
		case *ast.SelectorExpr:
			if _, ok := allowed[fun.Sel.Name]; ok {
				hasAudit = true
				return false
			}
			if target, ok := funcs[fun.Sel.Name]; ok && functionContainsAuditCall(target, funcs, allowed, visiting) {
				hasAudit = true
				return false
			}
		}
		return true
	})
	return hasAudit
}
