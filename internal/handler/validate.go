package handler

import (
	"encoding/json"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// rfc1123Subdomain is the RFC-1123 label/name shape Kubernetes (and Rancher)
// apply to imported cluster names: lowercase alphanumerics and hyphens, must
// start and end with an alphanumeric, max 63 chars. Registered as the custom
// `rfc1123` validator rule below so request structs can declare it as a tag.
var rfc1123Subdomain = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// validate is the process-wide validator instance. It is safe for concurrent
// use and caches struct reflection metadata, so it is initialised once.
var (
	validate     *validator.Validate
	validateOnce sync.Once
)

func validatorInstance() *validator.Validate {
	validateOnce.Do(func() {
		v := validator.New(validator.WithRequiredStructEnabled())

		// Surface the json tag name in field errors so the {"field": ...}
		// entries match the wire contract clients actually send, rather than
		// the Go struct field name.
		v.RegisterTagNameFunc(func(fld reflect.StructField) string {
			name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
			if name == "-" || name == "" {
				return fld.Name
			}
			return name
		})

		// Custom rule: rfc1123 — matches the cluster-name naming rules.
		_ = v.RegisterValidation("rfc1123", func(fl validator.FieldLevel) bool {
			return rfc1123Subdomain.MatchString(fl.Field().String())
		})

		validate = v
	})
	return validate
}

// fieldError is one entry in the uniform validation error envelope.
type fieldError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// decodeAndValidate decodes the JSON request body into a fresh value of type T
// and runs struct validation against its `validate:"..."` tags.
//
// On a body that cannot be decoded it writes a 400 invalid_body error. On a
// body that decodes but fails validation it writes a uniform 422 with the
// catalogued validation_error code and a per-field breakdown:
//
//	{"error": {"code": "validation_error", "message": "...",
//	           "fields": [{"field": "...", "rule": "...", "message": "..."}],
//	           "request_id": "..."}}
//
// It returns (zero, false) and has already written the response when either
// step fails; callers should simply return. On success it returns (value, true)
// and writes nothing.
func decodeAndValidate[T any](w http.ResponseWriter, r *http.Request, out *T) bool {
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return false
	}

	if err := validatorInstance().Struct(out); err != nil {
		writeValidationError(w, r, err)
		return false
	}
	return true
}

// writeValidationError renders a validator.ValidationErrors as the uniform 422
// envelope. A non-ValidationErrors error (e.g. an InvalidValidationError from a
// programming mistake) falls back to a generic 422 without a field breakdown.
func writeValidationError(w http.ResponseWriter, r *http.Request, err error) {
	verrs, ok := err.(validator.ValidationErrors)
	if !ok {
		RespondRequestError(w, r, http.StatusUnprocessableEntity, apierror.ValidationError, "Request failed validation")
		return
	}

	fields := make([]fieldError, 0, len(verrs))
	for _, fe := range verrs {
		fields = append(fields, fieldError{
			Field:   fe.Field(),
			Rule:    fe.Tag(),
			Message: validationMessage(fe),
		})
	}

	errObj := map[string]any{
		"code":    apierror.ValidationError,
		"message": "Request failed validation",
		"fields":  fields,
	}
	if r != nil {
		if requestID := middleware.GetRequestID(r.Context()); requestID != "" {
			errObj["request_id"] = requestID
		}
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": errObj})
}

// validationMessage renders a human-readable message for a single field error.
func validationMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fe.Field() + " is required"
	case "min":
		return fe.Field() + " must have at least " + fe.Param() + " item(s)"
	case "max":
		return fe.Field() + " must have at most " + fe.Param() + " item(s)"
	case "oneof":
		return fe.Field() + " must be one of: " + strings.ReplaceAll(fe.Param(), " ", ", ")
	case "rfc1123":
		return fe.Field() + " must be RFC-1123 (lowercase letters, digits, hyphens; start and end with an alphanumeric; max 63 chars)"
	case "email":
		return fe.Field() + " must be a valid email address"
	default:
		return fe.Field() + " failed the " + fe.Tag() + " rule"
	}
}
