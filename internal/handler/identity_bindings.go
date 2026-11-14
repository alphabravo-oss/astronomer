package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/pkg/safeconv"
)

type identityBindingQueries interface {
	CreateSharedIdentityBinding(context.Context, sqlc.CreateSharedIdentityBindingParams) (sqlc.SharedIdentityBinding, error)
	DeleteSharedIdentityBinding(context.Context, sqlc.DeleteSharedIdentityBindingParams) (int64, error)
	ListSharedIdentityBindings(context.Context, sqlc.ListSharedIdentityBindingsParams) ([]sqlc.SharedIdentityBinding, error)
	GetUserByIDForUpdate(context.Context, uuid.UUID) (sqlc.User, error)
}

func (h *SSOHandler) Bindings(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	actor := reqctx.UserUUID(r.Context())
	if !actor.Valid {
		apiutil.WriteError(w, r, 401, apiutil.AuthenticationRequired, "Authentication required")
		return
	}
	if !h.TransactionalAuditWired() {
		apiutil.WriteError(w, r, 503, apiutil.NotConfigured, "Identity administration unavailable")
		return
	}
	if r.Method == http.MethodGet {
		offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
		if r.URL.Query().Get("offset") == "" {
			offset = 0
			err = nil
		}
		if err != nil || offset < 0 || offset > 1000000 {
			apiutil.WriteError(w, r, 400, apiutil.ValidationError, "Invalid offset")
			return
		}
		var rows []sqlc.SharedIdentityBinding
		err = h.runTx(r.Context(), func(q SSOCallbackTx) error {
			var e error
			rows, e = q.ListSharedIdentityBindings(r.Context(), sqlc.ListSharedIdentityBindingsParams{Issuer: h.service.Issuer(), Limit: 100, Offset: safeconv.Int32(offset)})
			return e
		})
		if err != nil {
			apiutil.WriteError(w, r, 503, apiutil.LoadError, "Identity bindings unavailable")
			return
		}
		RespondJSON(w, 200, rows)
		return
	}
	var body struct {
		UserID  uuid.UUID `json:"user_id"`
		Subject string    `json:"subject"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.UserID == uuid.Nil || len(body.Subject) == 0 || len(body.Subject) > 255 {
		apiutil.WriteError(w, r, 400, apiutil.ValidationError, "A user ID and an exact external subject are required")
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		apiutil.WriteError(w, r, 400, apiutil.ValidationError, "Only one request object is allowed")
		return
	}
	action := "identity.binding_created"
	if r.Method == http.MethodDelete {
		action = "identity.binding_deleted"
	}
	err := h.runTx(r.Context(), func(q SSOCallbackTx) error {
		user, err := q.GetUserByIDForUpdate(r.Context(), body.UserID)
		if err != nil || (r.Method == http.MethodPost && (!user.IsActive || user.IsService)) {
			return errors.New("unavailable account")
		}
		if r.Method == http.MethodPost {
			_, err = q.CreateSharedIdentityBinding(r.Context(), sqlc.CreateSharedIdentityBindingParams{Issuer: h.service.Issuer(), Subject: body.Subject, UserID: body.UserID, LinkedBy: actor.Bytes})
		} else {
			_, err = q.DeleteSharedIdentityBinding(r.Context(), sqlc.DeleteSharedIdentityBindingParams{Issuer: h.service.Issuer(), Subject: body.Subject, UserID: body.UserID})
		}
		if err != nil {
			return err
		}
		return recordAuditOutboxAs(r, q, actor, action, "user", body.UserID.String(), user.Username, 200, map[string]any{"issuer": h.service.Issuer(), "subject": body.Subject})
	})
	if err != nil {
		apiutil.WriteError(w, r, 409, apiutil.InvalidRequest, "The identity binding could not be changed; check the account and existing binding")
		return
	}
	RespondJSON(w, 200, map[string]bool{"ok": true})
}
