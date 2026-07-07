package httpclient

import "testing"

func TestGuardPublicHost(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1/index.yaml",
		"https://localhost/v2/",
		"http://10.0.0.5/index.yaml",
		"http://192.168.1.1/index.yaml",
		"http://172.16.0.1/index.yaml",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/v2/",
		"http://0.0.0.0/",
	}
	for _, u := range blocked {
		if err := GuardPublicHost(u); err == nil {
			t.Errorf("GuardPublicHost(%q) = nil, want error (non-public host)", u)
		}
	}

	// Literal public IP needs no DNS resolution, keeping the test hermetic.
	if err := GuardPublicHost("https://8.8.8.8/index.yaml"); err != nil {
		t.Errorf("GuardPublicHost(public IP) = %v, want nil", err)
	}

	// A URL with no host (e.g. a file scheme) must be rejected.
	if err := GuardPublicHost("file:///etc/passwd"); err == nil {
		t.Errorf("GuardPublicHost(hostless) = nil, want error")
	}
}
