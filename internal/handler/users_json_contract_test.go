package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateUserV1AdditionalPropertiesCompatibility(t *testing.T) {
	q := &createUserTrackingQuerier{}
	h := NewResourceHandlerWithQueries(q, nil)
	wireCreateUserTestTx(h, q)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/users/", strings.NewReader(`{"email":"a@example.com","password":"ValidPassw0rd","displayName":"retained-client-extra","future":{"opaque":true}}`))
	w := httptest.NewRecorder()
	h.CreateUser(w, r)
	if w.Code != http.StatusCreated || q.createN != 1 || q.lastCreate.Username != "a@example.com" {
		t.Fatalf("published additive-v1 compatibility changed: status=%d writes=%d", w.Code, q.createN)
	}
}

func TestCreateUserJSONTrailingDocumentCannotWrite(t *testing.T) {
	q := &createUserTrackingQuerier{}
	h := NewResourceHandlerWithQueries(q, nil)
	wireCreateUserTestTx(h, q)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/users/", strings.NewReader(`{"email":"a@example.com","password":"ValidPassw0rd"}{"is_superuser":true}`))
	w := httptest.NewRecorder()
	h.CreateUser(w, r)
	if w.Code != http.StatusBadRequest || q.createN != 0 {
		t.Fatalf("trailing document reached durable writer: status=%d writes=%d", w.Code, q.createN)
	}
}

func TestDecodeStrictJSONBodyStandaloneSizeBound(t *testing.T) {
	var out struct {
		Name string `json:"name"`
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"`+strings.Repeat("a", 32<<20)+`"}`))
	if err := decodeStrictJSONBody(r, &out); err == nil {
		t.Fatal("standalone canonical decoder accepted an oversized document")
	}
}
