package apiutil

import (
	"net/http"

	"connectrpc.com/connect"
)

// StatusClientClosedRequest is nginx's conventional status for a request whose
// caller disconnected before a response was produced.
const StatusClientClosedRequest = 499

// ConnectCode returns the connect.Code a Connect handler reports for code.
// An unregistered code reports CodeInternal.
func ConnectCode(code Code) connect.Code {
	if s, ok := catalog[code]; ok {
		return s.connect
	}
	return connect.CodeInternal
}

// HTTPStatus returns code's canonical HTTP status. An unregistered code
// reports 500.
func HTTPStatus(code Code) int {
	if s, ok := catalog[code]; ok {
		return s.status
	}
	return http.StatusInternalServerError
}

// Known reports whether code is a catalog constant.
func Known(code Code) bool {
	_, ok := catalog[code]
	return ok
}

// Codes returns every catalog code (unordered). Tests use it to enumerate the
// closed set.
func Codes() []Code {
	out := make([]Code, 0, len(catalog))
	for code := range catalog {
		out = append(out, code)
	}
	return out
}

// HTTPStatusForConnectCode is the REST status a connect.Code maps to when no
// code-specific status applies.
func HTTPStatusForConnectCode(code connect.Code) int {
	switch code {
	case connect.CodeCanceled:
		return StatusClientClosedRequest
	case connect.CodeInvalidArgument, connect.CodeOutOfRange:
		return http.StatusBadRequest
	case connect.CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	case connect.CodeNotFound:
		return http.StatusNotFound
	case connect.CodeAlreadyExists, connect.CodeAborted, connect.CodeFailedPrecondition:
		return http.StatusConflict
	case connect.CodePermissionDenied:
		return http.StatusForbidden
	case connect.CodeResourceExhausted:
		return http.StatusTooManyRequests
	case connect.CodeUnimplemented:
		return http.StatusNotImplemented
	case connect.CodeUnavailable:
		return http.StatusServiceUnavailable
	case connect.CodeUnauthenticated:
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}

// ConnectCodeForHTTPStatus is the inverse family mapping used to build the
// catalog: the connect.Code that a REST status denotes.
func ConnectCodeForHTTPStatus(status int) connect.Code {
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return connect.CodeInvalidArgument
	case http.StatusUnauthorized:
		return connect.CodeUnauthenticated
	case http.StatusForbidden:
		return connect.CodePermissionDenied
	case http.StatusNotFound, http.StatusGone:
		return connect.CodeNotFound
	case http.StatusConflict, http.StatusPreconditionFailed, http.StatusPreconditionRequired, http.StatusLocked:
		return connect.CodeFailedPrecondition
	case http.StatusTooManyRequests:
		return connect.CodeResourceExhausted
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return connect.CodeDeadlineExceeded
	case StatusClientClosedRequest:
		return connect.CodeCanceled
	case http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return connect.CodeUnimplemented
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return connect.CodeUnavailable
	default:
		return connect.CodeInternal
	}
}
