package handler

import (
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
)

// validatePageOffset runs before a bounded query or in-memory slice. Existing
// cursor producers use it without rejecting their separately bound cursor.
func validatePageOffset(w http.ResponseWriter, r *http.Request) bool {
	if _, err := paging.LegacyOffset(r.URL.Query()); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.OffsetNotSupported, "offset must be one value in the supported range 0..10000; use this collection's opaque cursor for deeper traversal")
		return false
	}
	return true
}

// Legacy collections do not silently ignore an opaque continuation from a
// different API. The cursor-capable producers validate their own binding.
func validateLegacyPage(w http.ResponseWriter, r *http.Request) bool {
	if _, supplied := r.URL.Query()["cursor"]; supplied {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.CursorNotSupported, "this collection supports bounded legacy offset pagination; no cursor was accepted")
		return false
	}
	return validatePageOffset(w, r)
}
