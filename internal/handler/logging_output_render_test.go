package handler

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRenderOutputBlockSupportedTypes asserts every output type the logging UI
// offers renders a real Fluent Bit [OUTPUT] block (and not the "unsupported"
// comment). Previously Splunk/Datadog/CloudWatch/Syslog were silently dropped.
func TestRenderOutputBlockSupportedTypes(t *testing.T) {
	cases := []struct {
		outputType string
		cfg        map[string]any
		wantName   string
		wantParam  string // a param substring proving the block was rendered
	}{
		{"elasticsearch", map[string]any{"host": "es", "port": "9200"}, "Name es", "Host es"},
		{"loki", map[string]any{"host": "loki"}, "Name loki", "Host loki"},
		{"s3", map[string]any{"bucket": "b"}, "Name s3", "bucket b"},
		{"stdout", map[string]any{}, "Name stdout", "Match *"},
		{"splunk", map[string]any{"hec_url": "https://splunk.example.com:8088", "token": "tok"}, "Name splunk", "Splunk_Token tok"},
		{"datadog", map[string]any{"api_key": "k", "site": "datadoghq.eu"}, "Name datadog", "apikey k"},
		{"cloudwatch", map[string]any{"region": "us-west-2", "log_group": "/lg"}, "Name cloudwatch_logs", "log_group_name /lg"},
		{"syslog", map[string]any{"host": "syslog.example.com", "protocol": "tls"}, "Name syslog", "Mode tls"},
	}
	for _, c := range cases {
		t.Run(c.outputType, func(t *testing.T) {
			raw, _ := json.Marshal(c.cfg)
			out := renderOutputBlock(loggingOperationEnvelope{
				Name:          "test-" + c.outputType,
				OutputType:    c.outputType,
				Enabled:       true,
				Configuration: raw,
			})
			if strings.Contains(out, "unsupported output_type") {
				t.Fatalf("%s rendered as unsupported:\n%s", c.outputType, out)
			}
			if !strings.Contains(out, "[OUTPUT]") {
				t.Fatalf("%s missing [OUTPUT] block:\n%s", c.outputType, out)
			}
			if !strings.Contains(out, c.wantName) {
				t.Errorf("%s: want %q in:\n%s", c.outputType, c.wantName, out)
			}
			if !strings.Contains(out, c.wantParam) {
				t.Errorf("%s: want %q in:\n%s", c.outputType, c.wantParam, out)
			}
		})
	}
}

// Splunk HEC URL host/port parsing.
func TestOutputHostPort(t *testing.T) {
	h, p := outputHostPort(map[string]any{"hec_url": "https://splunk.example.com:8443"}, "hec_url", "8088")
	if h != "splunk.example.com" || p != "8443" {
		t.Fatalf("got host=%q port=%q, want splunk.example.com/8443", h, p)
	}
	// fall back to default port + host key when no URL.
	h, p = outputHostPort(map[string]any{"host": "h"}, "hec_url", "8088")
	if h != "h" || p != "8088" {
		t.Fatalf("got host=%q port=%q, want h/8088", h, p)
	}
}
