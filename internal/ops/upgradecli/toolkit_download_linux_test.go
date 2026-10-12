//go:build linux

package upgradecli

import (
	"errors"
	"net/http"
	"testing"
)

func TestToolkitDownloadRedirectsPreserveGuardAndLimitOrigins(t *testing.T) {
	for _, tc := range []struct {
		name, from, to string
		allowed        bool
	}{
		{"GitHub asset", "https://github.com/cli/cli/releases/download/v1/file", "https://release-assets.githubusercontent.com/asset?signature=fixture", true},
		{"same origin", "https://get.helm.sh/file", "https://get.helm.sh/other", true},
		{"asset same origin", "https://release-assets.githubusercontent.com/a", "https://release-assets.githubusercontent.com/b", true},
		{"unrelated host", "https://github.com/file", "https://example.com/file", false},
		{"unrelated source", "https://get.helm.sh/file", "https://release-assets.githubusercontent.com/file", false},
		{"reverse transition", "https://release-assets.githubusercontent.com/file", "https://github.com/file", false},
		{"downgrade", "https://github.com/file", "http://release-assets.githubusercontent.com/file", false},
		{"alternate port", "https://github.com/file", "https://release-assets.githubusercontent.com:8443/file", false},
		{"private target", "https://github.com/file", "https://127.0.0.1/file", false},
		{"suffix confusion", "https://github.com/file", "https://release-assets.githubusercontent.com.example.com/file", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			from, err := http.NewRequest(http.MethodGet, tc.from, nil)
			if err != nil {
				t.Fatal(err)
			}
			to, err := http.NewRequest(http.MethodGet, tc.to, nil)
			if err != nil {
				t.Fatal(err)
			}
			guardFailure := errors.New("shared destination guard rejected request")
			calls := 0
			redirect := toolkitDownloadRedirects(func(request *http.Request, via []*http.Request) error {
				calls++
				if request != to || len(via) != 1 || via[0] != from {
					t.Fatal("guard inputs changed")
				}
				return guardFailure
			})
			err = redirect(to, []*http.Request{from})
			if tc.allowed && (!errors.Is(err, guardFailure) || calls != 1) {
				t.Fatal("shared guard bypassed")
			}
			if !tc.allowed && (err == nil || calls != 0) {
				t.Fatal("unapproved origin reached shared guard")
			}
		})
	}
}

func TestToolkitDownloadRedirectsRejectMissingChain(t *testing.T) {
	redirect := toolkitDownloadRedirects(func(*http.Request, []*http.Request) error { t.Fatal("invalid chain reached guard"); return nil })
	if redirect(nil, nil) == nil {
		t.Fatal("missing chain accepted")
	}
}
