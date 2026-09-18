package platformsettings

import "testing"

func TestFeatureDefaults(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		key  string
		want bool
	}{
		{FeatureCatalog, true},
		{FeatureSecurity, true},
		{FeatureDelivery, true},
		{FeatureAlerting, true},
		{FeatureCharlie, false},
		{FeatureExtensions, false},
		{FeatureControlPlaneSnapshots, false},
	} {
		got, ok := FeatureDefault(test.key)
		if !ok || got != test.want {
			t.Fatalf("FeatureDefault(%q) = (%t, %t), want (%t, true)", test.key, got, ok, test.want)
		}
	}
	if value, ok := FeatureDefault("feature.unregistered"); ok || value {
		t.Fatalf("unknown feature default = (%t, %t), want (false, false)", value, ok)
	}
}
