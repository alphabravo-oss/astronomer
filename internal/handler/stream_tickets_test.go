package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

func TestStreamTicketHandler_CreateEventsTicket(t *testing.T) {
	store := auth.NewStreamTicketStore(time.Minute)
	h := NewStreamTicketHandler(store)
	userID := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/streams/tickets/", bytes.NewBufferString(`{"stream_type":"events"}`))
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: userID.String()}))
	w := httptest.NewRecorder()

	h.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data StreamTicketResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.Ticket == "" || resp.Data.ExpiresAt == "" {
		t.Fatalf("missing ticket response: %+v", resp.Data)
	}
	got, err := store.Validate(resp.Data.Ticket, auth.StreamKindEvents, uuid.Nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got != userID {
		t.Fatalf("ticket user = %s, want %s", got, userID)
	}
}

func TestStreamTicketHandler_ClusterStreamRequiresClusterID(t *testing.T) {
	h := NewStreamTicketHandler(auth.NewStreamTicketStore(time.Minute))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/streams/tickets/", bytes.NewBufferString(`{"stream_type":"logs"}`))
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: uuid.NewString()}))
	w := httptest.NewRecorder()

	h.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}
