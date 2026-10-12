package tunnel

import (
	"errors"
	"net/http"
)

// RejectPeerRedirect keeps an authenticated cross-pod request at its located
// peer. A peer response cannot select a different destination for caller
// credentials or a signed internal payload; peer routing uses the locator.
func RejectPeerRedirect(*http.Request, []*http.Request) error {
	return errors.New("cross-pod redirects are not permitted")
}
