package charlie

import "testing"

func TestProductDocumentationVersion(t *testing.T) {
	for input, want := range map[string]string{
		"v0.3.5-23-g392745f":       "0.3.5",
		"v0.3.5-23-g392745f-dirty": "0.3.5",
		"v1.2.3":                   "1.2.3",
		"1.2.3-rc.1":               "1.2.3-rc.1",
		"development":              "development",
	} {
		if got := productDocumentationVersion(input); got != want {
			t.Errorf("productDocumentationVersion(%q) = %q, want %q", input, got, want)
		}
	}
}
