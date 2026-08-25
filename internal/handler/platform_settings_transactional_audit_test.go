package handler

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestEveryPlatformSettingsMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("platform_settings.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"BatchUpdate": false, "Update": false, "Delete": false}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if _, tracked := want[fn.Name.Name]; !tracked {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executePlatformSettingsMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executePlatformSettingsMutation", name)
		}
	}
}

func TestCharlieRuntimeTransitionIsCompensatedWhenSettingAuditRollsBack(t *testing.T) {
	callerID := uuid.New()
	for _, tc := range []struct {
		name        string
		initial     string
		target      string
		wantEnable  int
		wantDisable int
	}{
		{name: "failed enable is disabled", target: "true", wantEnable: 1, wantDisable: 1},
		{name: "failed disable is re-enabled", initial: "true", target: "false", wantEnable: 1, wantDisable: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := newFakeSettingsQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
			if tc.initial != "" {
				q.rows["feature.charlie"] = sqlc.PlatformSetting{Key: "feature.charlie", Value: []byte(tc.initial)}
			}
			q.outboxErr = errors.New("audit unavailable")
			enables, disables := 0, 0
			h := NewPlatformSettingsHandler(q)
			h.SetRunTx(fakeSettingsRunTx(q))
			h.SetCharlieLifecycle(fakeCharlieSettingsLifecycle{
				enable:  func(context.Context, string) error { enables++; return nil },
				disable: func(context.Context, string) error { disables++; return nil },
			})
			r := withURLParam(
				authedRequest(http.MethodPut, "/api/v1/admin/settings/feature.charlie/", callerID, []byte(`{"value":`+tc.target+`}`)),
				"key", "feature.charlie",
			)
			w := httptest.NewRecorder()

			h.Update(w, r)

			if w.Code != http.StatusServiceUnavailable || enables != tc.wantEnable || disables != tc.wantDisable {
				t.Fatalf("status=%d enables=%d disables=%d body=%s", w.Code, enables, disables, w.Body.String())
			}
			row, err := q.GetPlatformSetting(context.Background(), "feature.charlie")
			if tc.initial == "" {
				if err == nil {
					t.Fatalf("failed enable retained row=%s", row.Value)
				}
			} else if err != nil || string(row.Value) != tc.initial {
				t.Fatalf("failed disable changed row=%s err=%v", row.Value, err)
			}
		})
	}
}
