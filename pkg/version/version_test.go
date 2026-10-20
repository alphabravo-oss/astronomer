package version

import (
	"strings"
	"testing"
)

func TestNormalizeGivesSemVerOneSpelling(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"2.0.1":               "v2.0.1",
		"v2.0.1":              "v2.0.1",
		"V2.0.1":              "v2.0.1",
		" 2.0.1-rc.1+meta\n":  "v2.0.1-rc.1+meta",
		"dev-abc1234":         "dev-abc1234",
		"v2.0.1-3-gabc-dirty": "v2.0.1-3-gabc-dirty",
		"":                    "",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if Normalize("2.0.1") != Normalize("v2.0.1") {
		t.Fatal("2.0.1 and v2.0.1 normalize differently")
	}
}

func TestVersionIsNormalizedAtInit(t *testing.T) {
	t.Parallel()
	if Version != Normalize(Version) {
		t.Fatalf("Version %q is not in canonical form", Version)
	}
	if Version[0] >= '0' && Version[0] <= '9' || !strings.HasPrefix(Version, "v") && !strings.HasPrefix(Version, "dev") {
		t.Fatalf("Version %q lacks the v prefix", Version)
	}
}
