package apiutil

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Domain sentinels. Domain packages wrap these (fmt.Errorf("cluster %s: %w",
// id, apiutil.ErrNotFound)) or return their own sentinels that wrap them;
// FromError maps them onto the catalog with errors.Is. The set is closed: a
// new kind of domain failure needs a new sentinel here and a row in
// sentinelCodes.
var (
	ErrNotFound           = errors.New("not found")
	ErrAlreadyExists      = errors.New("already exists")
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrFailedPrecondition = errors.New("failed precondition")
	ErrConflict           = errors.New("concurrent modification")
	ErrPermissionDenied   = errors.New("permission denied")
	ErrUnauthenticated    = errors.New("unauthenticated")
	ErrUnavailable        = errors.New("unavailable")
)

// sentinelCodes is checked in order; the first errors.Is match wins.
var sentinelCodes = []struct {
	err     error
	code    Code
	message string
}{
	{ErrNotFound, NotFound, "Resource not found"},
	{pgx.ErrNoRows, NotFound, "Resource not found"},
	{ErrAlreadyExists, AlreadyExists, "A resource with that identity already exists"},
	{ErrInvalidArgument, ValidationError, "The request failed validation"},
	{ErrFailedPrecondition, FailedPrecondition, "The resource is not in a state that allows this operation"},
	{ErrConflict, ConcurrentModification, "The resource changed concurrently; retry the request"},
	{ErrPermissionDenied, PermissionDenied, "You do not have permission to perform this action"},
	{ErrUnauthenticated, AuthenticationRequired, "Authentication is required to access this resource"},
	{ErrUnavailable, ServiceUnavailable, "The service is temporarily unavailable"},
	{context.DeadlineExceeded, DeadlineExceeded, "The request deadline was exceeded"},
	{context.Canceled, RequestCanceled, "The request was canceled"},
}

// Error is a classified API error: a catalog code, a client-safe message, the
// HTTP status to write, and the original cause (logged, never sent).
type Error struct {
	Code    Code
	Message string
	Status  int
	Cause   error
}

// New returns an *Error for code at its canonical HTTP status.
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message, Status: HTTPStatus(code)}
}

// Wrap is New with a log-only cause.
func Wrap(code Code, message string, cause error) *Error {
	e := New(code, message)
	e.Cause = cause
	return e
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return string(e.Code) + ": " + e.Message + ": " + e.Cause.Error()
	}
	return string(e.Code) + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

// ConnectCode is the connect.Code of e's catalog code.
func (e *Error) ConnectCode() connect.Code { return ConnectCode(e.Code) }

// ConnectError converts err for a Connect handler. Internal errors carry the
// scrubbed public message only; the cause stays server-side.
func ConnectError(err error) *connect.Error {
	apiErr := FromError(err)
	if apiErr == nil {
		return nil
	}
	return connect.NewError(apiErr.ConnectCode(), errors.New(publicMessage(apiErr.Status, apiErr.Message)))
}

// FromError classifies err into the catalog.
//
//   - nil stays nil;
//   - an *Error anywhere in the chain is returned as is;
//   - the domain sentinels, pgx.ErrNoRows and context errors map by errors.Is;
//   - *pgconn.PgError maps by SQLSTATE (unique → AlreadyExists, FK on delete →
//     ResourceInUse, FK on insert/update → InvalidReference, check/not-null/
//     data errors → ValidationError, serialization/deadlock →
//     ConcurrentModification, connection loss → DBUnavailable);
//   - anything else is Internal with err preserved as the cause.
func FromError(err error) *Error {
	if err == nil {
		return nil
	}
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	for _, s := range sentinelCodes {
		if errors.Is(err, s.err) {
			return Wrap(s.code, s.message, err)
		}
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if mapped := fromPgError(pgErr); mapped != nil {
			mapped.Cause = err
			return mapped
		}
	}
	return Wrap(InternalError, "The request could not be completed", err)
}

func fromPgError(pgErr *pgconn.PgError) *Error {
	switch pgErr.Code {
	case "23505": // unique_violation
		return New(AlreadyExists, "A resource with that identity already exists")
	case "23503": // foreign_key_violation
		if strings.HasPrefix(pgErr.Message, "insert or update on table") {
			return New(InvalidReference, "The request references a resource that does not exist")
		}
		return New(ResourceInUse, "The resource is still referenced by other resources")
	case "23001": // restrict_violation
		return New(ResourceInUse, "The resource is still referenced by other resources")
	case "23514", "23502", "22001", "22003", "22007", "22008", "22P02": // check, not-null, data exceptions
		return New(ValidationError, "The request failed validation")
	case "40001", "40P01": // serialization_failure, deadlock_detected
		return New(ConcurrentModification, "The resource changed concurrently; retry the request")
	case "57014": // query_canceled (statement_timeout)
		return New(DeadlineExceeded, "The request deadline was exceeded")
	}
	if strings.HasPrefix(pgErr.Code, "08") || pgErr.Code == "53300" || pgErr.Code == "57P01" {
		return New(DBUnavailable, "The database is temporarily unavailable")
	}
	return nil
}

// statusOf returns e's HTTP status, falling back to the catalog.
func (e *Error) statusOf() int {
	if e.Status != 0 {
		return e.Status
	}
	return HTTPStatus(e.Code)
}
