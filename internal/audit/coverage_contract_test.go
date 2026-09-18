package audit

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeyMutatingHandlersEmitAudit(t *testing.T) {
	expected := map[string][]string{
		"handler.AuthHandler":           {"Login", "Refresh", "Logout", "ChangePassword", "CreateToken", "RevokeToken"},
		"delivery.SourceHandler":        {"Create", "Delete", "Verify", "RotateCredential"},
		"delivery.BundleHandler":        {"Create", "CreateVersion"},
		"delivery.TargetHandler":        {"Create", "Update", "Delete", "Orphan"},
		"delivery.RolloutHandler":       {"Start", "Pause", "Resume", "Abort", "Retry", "Rollback", "Approve"},
		"delivery.DeploymentHandler":    {"Reconcile", "Suspend"},
		"handler.BackupHandler":         {"CreateStorageConfig", "DeleteStorageConfig", "UpdateStorageConfig", "CreateBackup", "DeleteBackup", "CreateSchedule", "DeleteSchedule", "UpdateSchedule", "TriggerSchedule", "CreateRestoreByBackup"},
		"handler.ClusterHandler":        {"Create", "Update", "Delete", "UpdateRegistryConfig"},
		"handler.DexHandler":            {"CreateConnector", "UpdateConnector", "DeleteConnector", "UpdateSettings", "Apply", "RegisterAsSSO"},
		"handler.GroupMappingsHandler":  {"Create", "Delete", "ResyncUser"},
		"handler.ProjectHandler":        {"Create", "Update", "Delete", "AddNamespace", "RemoveNamespace"},
		"handler.RBACHandler":           {"CreateGlobalRole", "UpdateGlobalRole", "DeleteGlobalRole", "CreateClusterRole", "UpdateClusterRole", "DeleteClusterRole", "CreateProjectRole", "UpdateProjectRole", "DeleteProjectRole", "CreateGlobalRoleBinding", "DeleteGlobalRoleBinding", "CreateClusterRoleBinding", "DeleteClusterRoleBinding", "CreateProjectRoleBinding", "DeleteProjectRoleBinding"},
		"handler.SecurityHandler":       {"CreateTemplate", "DeleteTemplate", "UpdateTemplate", "CreatePolicy", "ApplyPolicy", "DeletePolicy", "CreateScan"},
		"handler.CatalogHandler":        {"CreateInstallation", "DeleteInstalledChart", "UpgradeInstalledChart", "RollbackInstalledChart", "CreateRepo", "UpdateRepo", "DeleteRepo", "SyncRepo"},
		"handler.ProjectCatalogHandler": {"Create", "Subscribe", "Delete"},
		"handler.LoggingHandler":        {"CreateOutput", "UpdateOutput", "DeleteOutput", "CreatePipeline", "UpdatePipeline", "DeletePipeline"},
		"handler.MonitoringHandler":     {"UpdateBackendConfig", "UpdateEndpoint", "InstallSharedThanosStack", "UpgradeSharedThanosStack", "ReplaceSharedThanosStack", "UninstallSharedThanosStack", "InstallSharedAlertmanager", "UpgradeSharedAlertmanager", "ReplaceSharedAlertmanager", "UninstallSharedAlertmanager", "InstallSharedGrafanaStack", "UpgradeSharedGrafanaStack", "ReplaceSharedGrafanaStack", "UninstallSharedGrafanaStack", "UpdateClusterConfig", "InstallStack", "UpgradeStack", "ReplaceStack", "UninstallStack"},
		"handler.ResourceHandler":       {"CreateUser", "UpdateUser", "DeleteUser"},
		"handler.AlertingHandler":       {"CreateChannel", "UpdateChannel", "DeleteChannel", "CreateRule", "UpdateRule", "DeleteRule", "EnableRule", "DisableRule", "AcknowledgeEvent", "ResolveEvent", "CreateSilence", "ExpireSilence", "DeleteSilence"},
		"handler.ToolHandler":           {"Install", "Upgrade", "Uninstall", "Adopt"},
	}

	allowedAuditCalls := map[string]struct{}{
		"recordAudit":               {},
		"recordAuditAs":             {},
		"recordMandatoryAuditAs":    {},
		"recordProjectAudit":        {},
		"recordSecurityAuditOutbox": {},
		"executeMutation":           {},
		"executeMonitoringMutation": {},
		"newAuditIntent":            {},
		// This generated query commits the scan row, task intent, and
		// sanitized audit intent in one PostgreSQL statement.
		"CreateCISScanWithOutbox": {},
	}
	// These exact handlers commit their domain mutation, durable task intent,
	// and mandatory audit intent through recordAuditOutbox in one transaction.
	// Keep this allow-list handler-scoped: globally accepting recordAuditOutbox
	// would let an unrelated mutator satisfy the contract accidentally.
	atomicAuditOutboxHandlers := map[string]map[string]struct{}{
		"handler.DexHandler":            {"Apply": {}, "RegisterAsSSO": {}},
		"delivery.SourceHandler":        {"Verify": {}},
		"delivery.TargetHandler":        {"Delete": {}},
		"handler.CatalogHandler":        {"SyncRepo": {}},
		"handler.ProjectCatalogHandler": {"Create": {}, "Subscribe": {}, "Delete": {}},
		"handler.ResourceHandler":       {"CreateUser": {}, "UpdateUser": {}, "DeleteUser": {}},
	}

	packages := map[string][]*ast.File{}
	for identity, wantFuncs := range expected {
		packageName, receiver, _ := strings.Cut(identity, ".")
		files, loaded := packages[packageName]
		if !loaded {
			dir := "../handler"
			if packageName == "delivery" {
				dir = filepath.Join(dir, "delivery")
			}
			paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range paths {
				if strings.HasSuffix(path, "_test.go") {
					continue
				}
				file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
				if err != nil {
					t.Fatalf("parse %s: %v", path, err)
				}
				files = append(files, file)
			}
			packages[packageName] = files
		}
		funcs := auditFunctions(files)
		for _, name := range wantFuncs {
			fn := funcs[receiver+"."+name]
			allowedForHandler := allowedAuditCalls
			if handlers, pathAllowed := atomicAuditOutboxHandlers[identity]; pathAllowed {
				if _, handlerAllowed := handlers[name]; handlerAllowed {
					allowedForHandler = cloneAuditCalls(allowedAuditCalls)
					allowedForHandler["recordAuditOutbox"] = struct{}{}
				}
			}
			if !functionContainsAuditCall(fn, funcs, allowedForHandler, map[string]bool{}) {
				t.Errorf("%s: expected handler %s to emit audit", identity, name)
			}
		}
	}
}

func auditFunctions(files []*ast.File) map[string]*ast.FuncDecl {
	functions := map[string]*ast.FuncDecl{}
	for _, file := range files {
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			functions[auditFunctionKey(fn)] = fn
		}
	}
	return functions
}

func TestAuditCallGraphUsesReceiverIdentityAcrossFiles(t *testing.T) {
	var files []*ast.File
	for index, source := range []string{
		`package handler
		func (h *GoodHandler) Create() { h.lifecycle().apply() }
		func (h *GoodHandler) lifecycle() lifecycle[int] { return lifecycle[int]{} }
		func (h *BadHandler) Create() { h.helper() }
		func (h *BadHandler) helper() {}`,
		`package handler
		func (l lifecycle[T]) apply() { executeMutation() }
		func (h *OtherHandler) helper() { recordAudit() }
		func helper() { recordAudit() }`,
	} {
		file, err := parser.ParseFile(token.NewFileSet(), fmt.Sprintf("part%d.go", index), source, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	functions := auditFunctions(files)
	allowed := map[string]struct{}{"executeMutation": {}, "recordAudit": {}}
	if !functionContainsAuditCall(functions["GoodHandler.Create"], functions, allowed, map[string]bool{}) {
		t.Fatal("receiver-returned generic lifecycle must reach its transactional helper across files")
	}
	if functionContainsAuditCall(functions["BadHandler.Create"], functions, allowed, map[string]bool{}) {
		t.Fatal("another receiver or free function must not satisfy the expected handler audit contract")
	}
}

func auditFunctionKey(fn *ast.FuncDecl) string {
	if fn.Recv == nil {
		return fn.Name.Name
	}
	return auditTypeName(fn.Recv.List[0].Type) + "." + fn.Name.Name
}

func auditTypeName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return auditTypeName(value.X)
	case *ast.IndexExpr:
		return auditTypeName(value.X)
	case *ast.IndexListExpr:
		return auditTypeName(value.X)
	}
	return ""
}

// Follow receiver-qualified helper calls and factories returning a lifecycle
// value. A method on another handler with the same name cannot satisfy this
// call graph, even after both handlers are split across production files.
func auditCallTarget(expression ast.Expr, fn *ast.FuncDecl, funcs map[string]*ast.FuncDecl) *ast.FuncDecl {
	switch value := expression.(type) {
	case *ast.Ident:
		return funcs[value.Name]
	case *ast.IndexExpr:
		return auditCallTarget(value.X, fn, funcs)
	case *ast.IndexListExpr:
		return auditCallTarget(value.X, fn, funcs)
	case *ast.SelectorExpr:
		if name, ok := value.X.(*ast.Ident); ok && fn.Recv != nil {
			receiver := fn.Recv.List[0]
			if len(receiver.Names) > 0 && receiver.Names[0].Name == name.Name {
				return funcs[auditTypeName(receiver.Type)+"."+value.Sel.Name]
			}
		}
		if factory, ok := value.X.(*ast.CallExpr); ok {
			target := auditCallTarget(factory.Fun, fn, funcs)
			if target != nil && target.Type.Results != nil && len(target.Type.Results.List) > 0 {
				return funcs[auditTypeName(target.Type.Results.List[0].Type)+"."+value.Sel.Name]
			}
		}
	}
	return nil
}

func cloneAuditCalls(source map[string]struct{}) map[string]struct{} {
	cloned := make(map[string]struct{}, len(source)+1)
	for name := range source {
		cloned[name] = struct{}{}
	}
	return cloned
}

func functionContainsAuditCall(fn *ast.FuncDecl, funcs map[string]*ast.FuncDecl, allowed map[string]struct{}, visiting map[string]bool) bool {
	if fn == nil || fn.Body == nil {
		return false
	}
	key := auditFunctionKey(fn)
	if visiting[key] {
		return false
	}
	visiting[key] = true
	defer delete(visiting, key)

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
		case *ast.SelectorExpr:
			if _, ok := allowed[fun.Sel.Name]; ok {
				hasAudit = true
				return false
			}
		}
		if functionContainsAuditCall(auditCallTarget(call.Fun, fn, funcs), funcs, allowed, visiting) {
			hasAudit = true
			return false
		}
		return true
	})
	return hasAudit
}
