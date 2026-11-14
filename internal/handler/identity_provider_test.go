package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/identity"
	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

func TestIdentityProviderListingMatchesSDKWithoutContactingIssuer(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"local", "both"} {
		t.Run(mode, func(t *testing.T) {
			h := NewSSOHandler(identity.New(config.IdentitySettings{Mode: mode, Issuer: "https://unreachable.example.invalid"}, nil, nil), nil, "")
			w := httptest.NewRecorder()
			h.ListProviders(w, httptest.NewRequest("GET", "/api/v1/settings/sso/", nil))
			var providers []astroclient.UsersSettingsSSOProvider
			if err := json.Unmarshal(w.Body.Bytes(), &providers); err != nil {
				t.Fatal(err)
			}
			if mode == "local" && len(providers) != 0 {
				t.Fatal("local mode advertises SSO")
			}
			if mode == "both" && (len(providers) != 1 || providers[0].Id == nil || providers[0].Provider == nil || *providers[0].Provider != "identity") {
				t.Fatal("optional provider contract differs")
			}
		})
	}
}
