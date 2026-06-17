// Package apierror defines the canonical catalog of machine-readable error
// codes returned by the Astronomer REST API.
//
// Every error response produced by handler.RespondRequestError /
// handler.RespondError carries a stable `code` string in its body:
//
//	{"error": {"code": "<code>", "message": "<message>", "request_id": "..."}}
//
// Historically these codes were inline string literals scattered across ~2,000
// call sites and ~260 distinct spellings, with many near-duplicates
// (list_error vs list_failed, not_found vs cluster_not_found, etc.). This
// package seeds a typed catalog so that, going forward, handlers reference a
// single canonical constant per concept and the set of codes a client may
// observe is enumerable and documentable.
//
// The Code type is a defined string, so a constant of this type can be passed
// directly as the `code` argument to RespondRequestError without a conversion:
//
//	RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
//
// Codes are grouped below by the HTTP status family they typically accompany.
// A handful of codes (e.g. InvalidToken) legitimately appear under more than
// one status depending on context; the grouping reflects the dominant usage,
// not an exhaustive contract.
package apierror

// Code is a stable, machine-readable API error identifier. It is a defined
// string type so catalog constants can be passed wherever a `code string`
// argument is expected.
type Code = string

// --- Validation / bad client input (typically HTTP 400) ---

const (
	// InvalidBody indicates the request body could not be decoded (malformed
	// JSON or wrong shape).
	InvalidBody Code = "invalid_body"

	// InvalidID indicates a path or query identifier failed to parse (e.g. a
	// non-UUID id).
	InvalidID Code = "invalid_id"

	// ValidationError indicates the request was well-formed but failed
	// field-level validation rules.
	ValidationError Code = "validation_error"

	// InvalidRequest indicates a generically malformed or unsatisfiable
	// request that is not covered by a more specific validation code.
	InvalidRequest Code = "invalid_request"

	// InvalidName indicates a supplied name violates its naming constraints.
	InvalidName Code = "invalid_name"

	// InvalidToken indicates a supplied token is missing or malformed. (When
	// used in an authentication context this typically accompanies a 401.)
	InvalidToken Code = "invalid_token"
)

// --- Not found (HTTP 404) ---

const (
	// NotFound indicates the requested resource does not exist. Prefer this
	// generic code over entity-specific variants (cluster_not_found, etc.).
	NotFound Code = "not_found"
)

// --- Conflict / state and uniqueness violations (HTTP 409) ---

const (
	// Conflict indicates the request conflicts with the current state of the
	// resource (uniqueness violation, illegal state transition, etc.).
	Conflict Code = "conflict"
)

// --- Authentication and authorization (HTTP 401 / 403) ---

const (
	// AuthenticationRequired indicates the caller is not authenticated and a
	// credential is required (HTTP 401).
	AuthenticationRequired Code = "authentication_required"

	// Forbidden indicates the caller is authenticated but lacks permission for
	// the requested operation (HTTP 403).
	Forbidden Code = "forbidden"
)

// --- Server / IO / database failures (typically HTTP 500) ---

const (
	// InternalError indicates an unexpected server-side failure with no more
	// specific classification.
	InternalError Code = "internal_error"

	// DBError indicates a database query or transaction failed.
	DBError Code = "db_error"

	// ListError indicates a list/read query backing a collection endpoint
	// failed.
	ListError Code = "list_error"

	// CountError indicates a count query backing a paginated endpoint failed.
	CountError Code = "count_error"

	// CreateError indicates a resource could not be created due to a
	// server-side failure.
	CreateError Code = "create_error"

	// UpdateError indicates a resource could not be updated due to a
	// server-side failure.
	UpdateError Code = "update_error"

	// DeleteError indicates a resource could not be deleted due to a
	// server-side failure.
	DeleteError Code = "delete_error"
)
