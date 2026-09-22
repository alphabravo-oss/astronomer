package userpreferences

import (
	"fmt"
	"strings"
	"testing"
)

func TestStarredTypesValidation(t *testing.T) {
	for _, types := range [][]string{nil, {}, {"apps/deployments", "core/pods", "cert-manager.io/certificates"}} {
		prefs := Defaults()
		prefs.StarredTypes = types
		if err := prefs.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	tooMany := make([]string, 21)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("core/type%d", i)
	}
	for _, types := range [][]string{
		tooMany, {"apps/deployments", "apps/deployments"}, {"/pods"}, {"pods"},
		{"core/../pods"}, {"core/pods?token=value"}, {"Apps/deployments"}, {" core/pods"},
		{"core/" + strings.Repeat("p", 318)},
	} {
		prefs := Defaults()
		prefs.StarredTypes = types
		if prefs.Validate() == nil {
			t.Errorf("accepted invalid starred types %v", types)
		}
	}
}
