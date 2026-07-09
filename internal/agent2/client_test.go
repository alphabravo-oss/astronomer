package agent2

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TEST-04: shipped URL builder for remotedialer connect path.
func TestBuildWSURL_RewritesHTTPSAndEmbedsCluster(t *testing.T) {
	got, err := buildWSURL("https://mgmt.example.com/", "cluster-abc")
	if err != nil {
		t.Fatal(err)
	}
	want := "wss://mgmt.example.com/api/v1/connect/cluster-abc/"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBuildWSURL_HTTPToWS(t *testing.T) {
	got, err := buildWSURL("http://localhost:8080", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "ws://localhost:8080/api/v1/connect/c1/") {
		t.Fatalf("got %q", got)
	}
}

func TestBuildWSURL_RejectsBadScheme(t *testing.T) {
	if _, err := buildWSURL("ftp://nope", "c1"); err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

func TestConnectAndServe_RequiresInputs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := ConnectAndServe(ctx, nil, "", "c", "t", "", ""); err == nil {
		t.Fatal("empty server URL must fail")
	}
	if err := ConnectAndServe(ctx, nil, "https://x", "", "t", "", ""); err == nil {
		t.Fatal("empty cluster id must fail")
	}
	if err := ConnectAndServe(ctx, nil, "https://x", "c", "", "", ""); err == nil {
		t.Fatal("empty token must fail")
	}
}
