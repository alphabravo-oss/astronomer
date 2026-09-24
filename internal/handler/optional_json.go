package handler

import (
	"errors"
	"io"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// Empty bodies retain documented defaults; malformed bodies never do.
func decodeOptionalJSON[T any](w http.ResponseWriter, r *http.Request, out *T) bool {
	if r.Body == nil {
		return true
	}
	var decoded *T
	err := decodeStrictJSONBody(r, &decoded)
	if errors.Is(err, io.EOF) {
		return true
	}
	if err != nil || decoded == nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return false
	}
	*out = *decoded
	return true
}
