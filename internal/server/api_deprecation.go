package server

import (
	"net/http"
	"time"

	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

var apiV1AliasSunset = time.Date(2027, time.August, 23, 0, 0, 0, 0, time.UTC)

func deprecatedAPIAlias(successor string) func(http.Handler) http.Handler {
	return appmiddleware.DeprecatedRoute(apiV1AliasSunset, successor)
}
