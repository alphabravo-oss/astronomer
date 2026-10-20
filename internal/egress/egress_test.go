package egress

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestOutOfCluster(t *testing.T) {
	t.Parallel()
	for endpoint, want := range map[string]bool{
		"https://ghcr.io/x":                                true,
		"oci://ghcr.io/alphabravo-oss/catalog":             true,
		"mirror.corp:5000/astro":                           true,
		"http://tempo:4317":                                false,
		"tempo.monitoring.svc:4317":                        false,
		"astronomer-zot.astronomer.svc.cluster.local:5000": false,
		"localhost:4317":                                   false,
		"127.0.0.1:4317":                                   false,
		"10.0.0.5:4317":                                    true,
		"":                                                 false,
	} {
		if got := OutOfCluster(endpoint); got != want {
			t.Errorf("OutOfCluster(%q) = %v, want %v", endpoint, got, want)
		}
	}
}

func TestLogLine(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	var set Set
	set.Add("https://otel.example.com:4317")
	set.Add("tempo:4317")
	set.Add("https://otel.example.com:4317")
	set.Dynamic("webhooks:database")
	set.Log(context.Background(), logger, "server")
	line := buf.String()
	if !strings.Contains(line, `msg="egress endpoints"`) || !strings.Contains(line, "service=server") || !strings.Contains(line, "otel.example.com") || strings.Contains(line, "tempo") || !strings.Contains(line, "webhooks:database") {
		t.Fatalf("unexpected line %q", line)
	}
}
