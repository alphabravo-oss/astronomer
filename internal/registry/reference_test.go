package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestRehost(t *testing.T) {
	t.Parallel()
	pinned := "@sha256:" + "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
	cases := map[string]string{
		"busybox:1.36":                                          "zot.astronomer.svc:5000/busybox:1.36",
		"docker.io/library/busybox:1.36":                        "zot.astronomer.svc:5000/busybox:1.36",
		"dexidp/dex:v2.45.1":                                    "zot.astronomer.svc:5000/dexidp/dex:v2.45.1",
		"ghcr.io/alphabravo-oss/astronomer-go-agent:v2.0.1":     "zot.astronomer.svc:5000/alphabravo-oss/astronomer-go-agent:v2.0.1",
		"ghcr.io/fluxcd/helm-controller" + pinned:               "zot.astronomer.svc:5000/fluxcd/helm-controller" + pinned,
		"oci://ghcr.io/alphabravo-oss/astronomer-catalog":       "oci://zot.astronomer.svc:5000/alphabravo-oss/astronomer-catalog",
		"localhost:5000/x/y:z":                                  "zot.astronomer.svc:5000/x/y:z",
		"zot.astronomer.svc:5000/alphabravo-oss/astronomer:1.0": "zot.astronomer.svc:5000/alphabravo-oss/astronomer:1.0",
	}
	for in, want := range cases {
		got, err := Rehost(in, "zot.astronomer.svc:5000")
		if err != nil || got != want {
			t.Errorf("Rehost(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	got, err := Rehost("quay.io/prometheus/node-exporter:v1", "mirror.corp/astro")
	if err != nil || got != "mirror.corp/astro/prometheus/node-exporter:v1" {
		t.Errorf("prefix rehost = %q, %v", got, err)
	}
	for _, bad := range []string{"", "https://ghcr.io/x", "ghcr.io/", "a b"} {
		if _, err := Rehost(bad, "zot:5000"); err == nil {
			t.Errorf("Rehost(%q) accepted", bad)
		}
	}
}

func TestParseListAndValidate(t *testing.T) {
	t.Parallel()
	list, err := ParseList(" mirror.corp:443/astro , registry.example.com ,")
	if err != nil || len(list) != 2 || list[0] != "mirror.corp:443/astro" {
		t.Fatalf("ParseList = %v, %v", list, err)
	}
	if list, err := ParseList(""); err != nil || len(list) != 0 {
		t.Fatalf("empty fallback list must be empty: %v %v", list, err)
	}
	for _, bad := range []string{"https://ghcr.io", "user@ghcr.io", "ghcr.io/", "Upper.io", "busybox"} {
		if err := ValidateBase(bad); err == nil {
			t.Errorf("ValidateBase(%q) accepted", bad)
		}
	}
}

func TestNewRefusesUnsafeConfig(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{Fallbacks: []string{"mirror.corp"}}, VerifyDigestPinned, nil); err == nil {
		t.Fatal("fallbacks without a local registry must be refused")
	}
	if _, err := New(Config{Local: "zot.ns.svc:5000", Fallbacks: []string{"mirror.corp"}}, nil, nil); err == nil {
		t.Fatal("fallbacks without a verifier must be refused")
	}
	r, err := New(Config{}, nil, nil)
	if err != nil || r.Enabled() {
		t.Fatalf("empty config is the online profile: %v", err)
	}
	if got := r.Localize("ghcr.io/x/y:1"); got != "ghcr.io/x/y:1" {
		t.Fatalf("online Localize rewrote %q", got)
	}
	res, err := r.Resolve(context.Background(), "ghcr.io/x/y:1")
	if err != nil || res.Layer != LayerOnline {
		t.Fatalf("online Resolve = %+v %v", res, err)
	}
}

func TestVerifyDigestPinned(t *testing.T) {
	t.Parallel()
	d := digest.FromString("x")
	desc := ocispec.Descriptor{Digest: d}
	if err := VerifyDigestPinned(context.Background(), "ghcr.io/a/b@"+d.String(), "m/a/b@"+d.String(), desc); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDigestPinned(context.Background(), "ghcr.io/a/b:1", "m/a/b:1", desc); err == nil {
		t.Fatal("tag-only fallback accepted")
	}
	other := ocispec.Descriptor{Digest: digest.FromString("y")}
	if err := VerifyDigestPinned(context.Background(), "ghcr.io/a/b@"+d.String(), "m/a/b@"+d.String(), other); err == nil {
		t.Fatal("digest mismatch accepted")
	}
	if !errors.Is(ErrFailClosed, ErrFailClosed) {
		t.Fatal("sentinel")
	}
}
