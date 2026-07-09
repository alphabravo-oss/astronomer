package httpclient

import (
	"net"
	"testing"
)

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
		// SEC-R08: RFC 6598 CGNAT shared address space.
		"http://100.64.0.1/",
		"https://100.127.255.254/v2/",
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

// SEC-R08: isPublicIP must reject RFC 6598 CGNAT 100.64.0.0/10.
func TestIsPublicIP_RejectsCGNAT(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"100.64.0.0", false},
		{"100.64.0.1", false},
		{"100.127.255.254", false},
		{"100.63.255.255", true},  // just below CGNAT
		{"100.128.0.0", true},     // just above CGNAT
		{"8.8.8.8", true},
		{"10.0.0.1", false},
		{"169.254.169.254", false},
		{"127.0.0.1", false},
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("ParseIP(%q) = nil", tc.ip)
		}
		if got := isPublicIP(ip); got != tc.want {
			t.Errorf("isPublicIP(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestIsDialAllowed_AllowPrivateStillBlocksMetadata(t *testing.T) {
	// AllowPrivate opens RFC1918 / CGNAT for in-cluster backends but must
	// never open loopback or link-local/metadata.
	if isDialAllowed(net.ParseIP("10.0.0.5"), true) != true {
		t.Error("AllowPrivate should permit 10.0.0.5")
	}
	if isDialAllowed(net.ParseIP("100.64.0.1"), true) != true {
		t.Error("AllowPrivate should permit CGNAT")
	}
	if isDialAllowed(net.ParseIP("127.0.0.1"), true) {
		t.Error("AllowPrivate must still block loopback")
	}
	if isDialAllowed(net.ParseIP("169.254.169.254"), true) {
		t.Error("AllowPrivate must still block link-local metadata")
	}
	if isDialAllowed(net.ParseIP("10.0.0.5"), false) {
		t.Error("public-only mode must block private")
	}
}
