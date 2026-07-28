package agent

import "testing"

func TestParseImageReferenceAcceptsRealisticReferences(t *testing.T) {
	cases := map[string]imageReference{
		"example.com/astronomer-agent:v1.2.3":         {Repository: "example.com/astronomer-agent", Tag: "v1.2.3"},
		"registry.example:5000/ab/astronomer-ag:v0.2": {Repository: "registry.example:5000/ab/astronomer-ag", Tag: "v0.2"},
		"ghcr.io/ab/agent@sha256:" + strings64():      {Repository: "ghcr.io/ab/agent", Digest: "sha256:" + strings64()},
		"agent:v1":                                    {Repository: "agent", Tag: "v1"},
	}
	for raw, want := range cases {
		got, err := parseImageReference(raw)
		if err != nil {
			t.Fatalf("parseImageReference(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("parseImageReference(%q) = %+v, want %+v", raw, got, want)
		}
	}
}

func TestParseImageReferenceRejectsUnsafeReferences(t *testing.T) {
	for _, raw := range []string{
		"",
		"   ",
		"example.com/astronomer-agent",           // unqualified: means :latest to the kubelet
		"example.com/astronomer agent:v1",        // whitespace
		"example.com/astronomer-agent:v1\nfoo:v", // newline injection
		"example.com//agent:v1",                  // empty segment
		"example.com/../agent:v1",                // traversal
		"example.com/agent@sha256:deadbeef",      // truncated digest
		"example.com/agent@md5:" + strings64(),   // unsupported algorithm
		"example.com/agent:-badtag",              // tag may not start with '-'
		"example.com:notaport/agent:v1",
		"exämple.com/agent:v1", // non-ASCII
	} {
		if ref, err := parseImageReference(raw); err == nil {
			t.Fatalf("parseImageReference(%q) = %+v, want an error", raw, ref)
		}
	}
}

func TestValidateAgentImageFailsClosedWithoutAnAllowedRepository(t *testing.T) {
	if _, err := validateAgentImage(testTargetImage, imagePolicy{}); err == nil {
		t.Fatal("validateAgentImage with no allowed repository returned nil; it must fail closed")
	}
}

// A prefix allow-list would accept "example.com/astronomer-agent-attacker" for
// the repository "example.com/astronomer-agent". The match is exact.
func TestValidateAgentImageMatchesRepositoryExactly(t *testing.T) {
	policy := imagePolicy{AllowedRepository: "example.com/astronomer-agent"}
	if _, err := validateAgentImage("example.com/astronomer-agent-attacker:v1", policy); err == nil {
		t.Fatal("prefix-matching repository was accepted")
	}
	if _, err := validateAgentImage("example.com/astronomer-agent:v1.2.3", policy); err != nil {
		t.Fatalf("exact repository rejected: %v", err)
	}
}

func TestValidateAgentImageDigestsAreAlwaysAllowed(t *testing.T) {
	policy := imagePolicy{AllowedRepository: "example.com/astronomer-agent"}
	if _, err := validateAgentImage("example.com/astronomer-agent@sha256:"+strings64(), policy); err != nil {
		t.Fatalf("digest reference rejected: %v", err)
	}
}

func TestImageRepositoryOfToleratesUnqualifiedReferences(t *testing.T) {
	cases := map[string]string{
		"example.com/astronomer-agent:v1.2.3":                "example.com/astronomer-agent",
		"example.com/astronomer-agent":                       "example.com/astronomer-agent",
		"registry.example:5000/astronomer-agent":             "registry.example:5000/astronomer-agent",
		"example.com/astronomer-agent@sha256:" + strings64(): "example.com/astronomer-agent",
		"not a reference":                                    "",
	}
	for raw, want := range cases {
		if got := imageRepositoryOf(raw); got != want {
			t.Fatalf("imageRepositoryOf(%q) = %q, want %q", raw, got, want)
		}
	}
}

func strings64() string {
	const hex = "0123456789abcdef"
	out := make([]byte, 64)
	for i := range out {
		out[i] = hex[i%len(hex)]
	}
	return string(out)
}
