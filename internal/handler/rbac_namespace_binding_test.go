package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestCreateClusterRoleBinding_NamespaceValidation locks in the DNS-1123
// validation on the namespace field of the cluster role-binding create
// handler. An empty namespace means cluster-wide (allowed); a valid label is
// persisted and echoed; an invalid label fails closed with a 400 and writes
// nothing. The handler is constructed without SetAuthorization, so the
// escalation guard short-circuits (engine==nil) and we exercise validation +
// persistence in isolation.
func TestCreateClusterRoleBinding_NamespaceValidation(t *testing.T) {
	callerID := uuid.New()
	userID := uuid.New()
	roleID := uuid.New()
	clusterID := uuid.New()

	cases := []struct {
		name          string
		namespace     string
		wantStatus    int
		wantNamespace string
		wantPersisted int
	}{
		{
			name:          "valid namespace label",
			namespace:     "kube-system",
			wantStatus:    http.StatusCreated,
			wantNamespace: "kube-system",
			wantPersisted: 1,
		},
		{
			name:          "empty namespace is cluster-wide",
			namespace:     "",
			wantStatus:    http.StatusCreated,
			wantNamespace: "",
			wantPersisted: 1,
		},
		{
			name:          "invalid namespace label rejected",
			namespace:     "Bad_NS",
			wantStatus:    http.StatusBadRequest,
			wantPersisted: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := newFakeRBACAuditQuerier()
			h := NewRBACHandler(q)

			body := []byte(fmt.Sprintf(
				`{"user_id":"%s","role_id":"%s","cluster_id":"%s","namespace":"%s"}`,
				userID, roleID, clusterID, tc.namespace))
			req := authedRequest(http.MethodPost, "/api/v1/rbac/cluster-role-bindings/", callerID, body)
			rec := httptest.NewRecorder()
			h.CreateClusterRoleBinding(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if n := len(q.clusterBindings); n != tc.wantPersisted {
				t.Fatalf("persisted bindings = %d, want %d", n, tc.wantPersisted)
			}

			if tc.wantStatus == http.StatusCreated {
				var resp struct {
					Data struct {
						Namespace string `json:"namespace"`
					} `json:"data"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
				}
				if resp.Data.Namespace != tc.wantNamespace {
					t.Fatalf("response namespace = %q, want %q", resp.Data.Namespace, tc.wantNamespace)
				}
				for _, b := range q.clusterBindings {
					if b.Namespace != tc.wantNamespace {
						t.Fatalf("persisted namespace = %q, want %q", b.Namespace, tc.wantNamespace)
					}
				}
			}

			if tc.wantStatus == http.StatusBadRequest {
				var resp struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode error response: %v; body=%s", err, rec.Body.String())
				}
				if resp.Error.Code != "validation_error" {
					t.Fatalf("error code = %q, want validation_error; body=%s", resp.Error.Code, rec.Body.String())
				}
			}
		})
	}
}
