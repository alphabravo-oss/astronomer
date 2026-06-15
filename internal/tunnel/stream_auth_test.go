package tunnel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

func TestAuthenticateStreamRequestUsesTicketKindAndClusterScope(t *testing.T) {
	store := auth.NewStreamTicketStore(time.Minute)
	userID := uuid.New()
	clusterID := uuid.New()
	token, _, err := store.Issue(userID, auth.StreamKindLogs, clusterID)
	if err != nil {
		t.Fatalf("issue stream ticket: %v", err)
	}

	req := requestWithClusterID("/api/v1/ws/logs/"+clusterID.String()+"/default/pod/container/?ticket="+token, clusterID)
	got, ok := authenticateStreamRequest(req, nil, nil, store, auth.StreamKindLogs)
	if !ok {
		t.Fatal("expected logs ticket to authenticate")
	}
	if got != userID {
		t.Fatalf("userID = %s, want %s", got, userID)
	}

	wrongKindToken, _, err := store.Issue(userID, auth.StreamKindLogs, clusterID)
	if err != nil {
		t.Fatalf("issue wrong-kind stream ticket: %v", err)
	}
	req = requestWithClusterID("/api/v1/ws/exec/"+clusterID.String()+"/default/pod/container/?ticket="+wrongKindToken, clusterID)
	if got, ok := authenticateStreamRequest(req, nil, nil, store, auth.StreamKindExec); ok || got != uuid.Nil {
		t.Fatalf("expected wrong kind to reject, got userID=%s ok=%v", got, ok)
	}

	otherClusterToken, _, err := store.Issue(userID, auth.StreamKindLogs, clusterID)
	if err != nil {
		t.Fatalf("issue wrong-cluster stream ticket: %v", err)
	}
	req = requestWithClusterID("/api/v1/ws/logs/"+uuid.NewString()+"/default/pod/container/?ticket="+otherClusterToken, uuid.New())
	if got, ok := authenticateStreamRequest(req, nil, nil, store, auth.StreamKindLogs); ok || got != uuid.Nil {
		t.Fatalf("expected wrong cluster to reject, got userID=%s ok=%v", got, ok)
	}
}

func requestWithClusterID(rawURL string, clusterID uuid.UUID) *http.Request {
	req := httptest.NewRequest(http.MethodGet, rawURL, nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("cluster_id", clusterID.String())
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}
