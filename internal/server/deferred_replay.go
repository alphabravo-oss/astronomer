package server

import (
	"context"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/deferredreplay"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

type deferredReplayCipher = deferredreplay.Cipher
type deferredReplayQueries = deferredreplay.Queries

// newDeferredHTTPReplayers remains the server-local composition seam while
// the implementation lives below server so runtime integration can exercise
// the exact production replayer without creating an import cycle.
func newDeferredHTTPReplayers(router http.Handler, jwtManager *auth.JWTManager, cipher deferredReplayCipher, queries deferredReplayQueries) (map[string]tasks.DeferredReplayer, error) {
	return deferredreplay.NewHTTPReplayers(router, jwtManager, cipher, queries)
}

func replayDeferredHTTPRequest(ctx context.Context, router http.Handler, jwtManager *auth.JWTManager, cipher deferredReplayCipher, queries deferredReplayQueries, row sqlc.DeferredOperation) error {
	return deferredreplay.ReplayHTTPRequest(ctx, router, jwtManager, cipher, queries, row)
}
