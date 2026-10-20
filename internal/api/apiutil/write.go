package apiutil

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

// Body is the wire shape of every API error response.
type Body struct {
	Error BodyError `json:"error"`
}

// BodyError is the inner object of Body.
type BodyError struct {
	Code      Code   `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// WriteError writes the error envelope for an explicit status and catalog
// code. It is the only function in the API that writes an error body.
//
// For status >= 500 the message is replaced with a fixed public message and
// the original is logged with the request id that is returned to the client.
// A request whose context was canceled by the client gets a bare 499.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code Code, message string) {
	write(w, r, status, code, message, nil)
}

// Respond classifies err with FromError and writes it at the classified
// status. Internal errors never echo err; it is logged as the cause.
func Respond(w http.ResponseWriter, r *http.Request, err error) {
	apiErr := FromError(err)
	if apiErr == nil {
		apiErr = New(InternalError, "The request could not be completed")
	}
	write(w, r, apiErr.statusOf(), apiErr.Code, apiErr.Message, apiErr.Cause)
}

func write(w http.ResponseWriter, r *http.Request, status int, code Code, message string, cause error) {
	if r != nil && errors.Is(r.Context().Err(), context.Canceled) {
		w.WriteHeader(StatusClientClosedRequest)
		return
	}
	requestID := ""
	if r != nil {
		requestID = reqctx.RequestID(r.Context())
	}
	if status >= http.StatusInternalServerError {
		if requestID == "" {
			requestID = uuid.NewString()
		}
		attrs := []any{"request_id", requestID, "status", status, "code", string(code), "internal_detail", message}
		if cause != nil {
			attrs = append(attrs, "cause", cause.Error())
		}
		slog.Error("request failed", attrs...)
	}
	message = publicMessage(status, message)
	observability.RecordRequestError(observability.RoutePattern(r), status)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Body{Error: BodyError{Code: code, Message: message, RequestID: requestID}})
}

// publicMessage scrubs server-side failure detail from 5xx responses.
func publicMessage(status int, message string) string {
	switch {
	case status < http.StatusInternalServerError:
		return message
	case status == http.StatusBadGateway:
		return "An upstream service could not complete the request"
	case status == http.StatusServiceUnavailable, status == http.StatusGatewayTimeout:
		return "The service is temporarily unavailable"
	default:
		return "The request could not be completed"
	}
}
