package handler

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestScopeParsersRejectInvalidScopeWithSharedEnvelope(t *testing.T) {
	for _, parse := range []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request) (uuid.UUID, bool)
	}{
		{"cluster_id", parseClusterID},
		{"project_id", parseProjectID},
	} {
		for _, value := range []string{"", "malformed", uuid.Nil.String()} {
			t.Run(parse.name+"/"+value, func(t *testing.T) {
				r := httptest.NewRequest(http.MethodGet, "/?"+parse.name+"="+uuid.NewString(), nil)
				route := chi.NewRouteContext()
				route.URLParams.Add(parse.name, value)
				route.URLParams.Add("id", uuid.NewString())
				r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, route))
				w := httptest.NewRecorder()
				if _, ok := parse.fn(w, r); ok {
					t.Fatal("invalid scope accepted")
				}
				if w.Code != http.StatusBadRequest {
					t.Fatalf("status=%d", w.Code)
				}
				var body struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Error.Code != "invalid_id" {
					t.Fatalf("error=%q", body.Error.Code)
				}
			})
		}
	}
}

func TestOptionalClusterScopeDistinguishesFleetFilterAndInvalidInput(t *testing.T) {
	clusterID := uuid.New()
	otherClusterID := uuid.New()
	tests := []struct {
		name       string
		route      string
		query      string
		want       uuid.UUID
		present    bool
		ok         bool
		wantStatus int
	}{
		{name: "fleet wide", ok: true, wantStatus: http.StatusOK},
		{name: "query filter", query: "cluster_id=" + clusterID.String(), want: clusterID, present: true, ok: true, wantStatus: http.StatusOK},
		{name: "route filter", route: clusterID.String(), want: clusterID, present: true, ok: true, wantStatus: http.StatusOK},
		{name: "matching route and query", route: clusterID.String(), query: "cluster_id=" + clusterID.String(), want: clusterID, present: true, ok: true, wantStatus: http.StatusOK},
		{name: "malformed query", query: "cluster_id=bad", present: true, wantStatus: http.StatusBadRequest},
		{name: "nil query", query: "cluster_id=" + uuid.Nil.String(), present: true, wantStatus: http.StatusBadRequest},
		{name: "duplicate query", query: "cluster_id=" + clusterID.String() + "&cluster_id=" + clusterID.String(), present: true, wantStatus: http.StatusBadRequest},
		{name: "conflicting route and query", route: clusterID.String(), query: "cluster_id=" + otherClusterID.String(), present: true, wantStatus: http.StatusBadRequest},
		{name: "malformed route does not fall back", route: "bad", query: "cluster_id=" + clusterID.String(), present: true, wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/?"+test.query, nil)
			route := chi.NewRouteContext()
			route.URLParams.Add("cluster_id", test.route)
			request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, route))
			response := httptest.NewRecorder()

			got, present, ok := parseOptionalClusterID(response, request)

			if got != test.want || present != test.present || ok != test.ok {
				t.Fatalf("got id=%s present=%t ok=%t; want id=%s present=%t ok=%t", got, present, ok, test.want, test.present, test.ok)
			}
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if !ok && !strings.Contains(response.Body.String(), `"code":"invalid_id"`) {
				t.Fatalf("invalid scope did not use canonical envelope: %s", response.Body.String())
			}
		})
	}
}

func TestRequiredClusterRoutesUseCanonicalParser(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") || path == "request_scope.go" {
			continue
		}
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "URLParam" {
				return true
			}
			literal, ok := call.Args[1].(*ast.BasicLit)
			if !ok {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err == nil && value == "cluster_id" {
				t.Errorf("%s:%d reads required cluster_id directly; use parseClusterID", path, fileSet.Position(call.Pos()).Line)
			}
			return true
		})
	}
}
