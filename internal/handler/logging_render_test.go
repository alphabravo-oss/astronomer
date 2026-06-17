package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestRenderConfigMapDataElasticsearchOutput verifies an elasticsearch
// output renders an [OUTPUT] Name es block with Host/Port/Index pulled
// from configuration JSON.
func TestRenderConfigMapDataElasticsearchOutput(t *testing.T) {
	h := &LoggingHandler{}
	env := loggingOperationEnvelope{
		ClusterID:  uuid.New().String(),
		TargetID:   uuid.New().String(),
		TargetType: "output",
		Name:       "prod-es",
		OutputType: "elasticsearch",
		Enabled:    true,
		Configuration: json.RawMessage(`{
			"host": "es.example.com",
			"port": "9200",
			"index": "astronomer",
			"http_user": "admin",
			"http_passwd": "${ES_PASSWORD}"
		}`),
	}
	data, err := h.renderConfigMapData(env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	conf := data["output.conf"]
	if conf == "" {
		t.Fatalf("expected output.conf, keys=%v", keysOf(toAny(data)))
	}
	for _, want := range []string{"[OUTPUT]", "Name es", "Host es.example.com", "Port 9200", "Index astronomer", "HTTP_User admin", "HTTP_Passwd ${ES_PASSWORD}"} {
		if !strings.Contains(conf, want) {
			t.Errorf("output.conf missing %q\n--- got ---\n%s", want, conf)
		}
	}
	meta := data["meta.json"]
	if !strings.Contains(meta, `"output_type": "elasticsearch"`) {
		t.Errorf("meta.json missing output_type; got %s", meta)
	}
	if !strings.Contains(meta, `"generated_at"`) {
		t.Errorf("meta.json missing generated_at; got %s", meta)
	}
}

// TestRenderConfigMapDataLokiOutput verifies the loki renderer.
func TestRenderConfigMapDataLokiOutput(t *testing.T) {
	h := &LoggingHandler{}
	env := loggingOperationEnvelope{
		ClusterID:     uuid.New().String(),
		TargetID:      uuid.New().String(),
		TargetType:    "output",
		Name:          "loki-shared",
		OutputType:    "loki",
		Enabled:       true,
		Configuration: json.RawMessage(`{"host": "loki.example.com", "port": "3100", "labels": "job=astronomer", "tenant_id": "team-a"}`),
	}
	data, err := h.renderConfigMapData(env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	conf := data["output.conf"]
	for _, want := range []string{"Name loki", "Host loki.example.com", "Port 3100", "Labels job=astronomer", "tenant_id team-a"} {
		if !strings.Contains(conf, want) {
			t.Errorf("output.conf missing %q\n--- got ---\n%s", want, conf)
		}
	}
}

// TestRenderConfigMapDataS3Output verifies the s3 renderer pulls
// bucket/region/total_file_size from the configuration JSON.
func TestRenderConfigMapDataS3Output(t *testing.T) {
	h := &LoggingHandler{}
	env := loggingOperationEnvelope{
		ClusterID:     uuid.New().String(),
		TargetID:      uuid.New().String(),
		TargetType:    "output",
		Name:          "archive-s3",
		OutputType:    "s3",
		Enabled:       true,
		Configuration: json.RawMessage(`{"bucket": "logs-archive", "region": "us-west-2", "total_file_size": "50M"}`),
	}
	data, err := h.renderConfigMapData(env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	conf := data["output.conf"]
	for _, want := range []string{"Name s3", "bucket logs-archive", "region us-west-2", "total_file_size 50M"} {
		if !strings.Contains(conf, want) {
			t.Errorf("output.conf missing %q\n--- got ---\n%s", want, conf)
		}
	}
}

// TestRenderConfigMapDataStdoutOutput verifies stdout falls back to defaults
// and renders Name stdout / Match *.
func TestRenderConfigMapDataStdoutOutput(t *testing.T) {
	h := &LoggingHandler{}
	env := loggingOperationEnvelope{
		ClusterID:  uuid.New().String(),
		TargetID:   uuid.New().String(),
		TargetType: "output",
		Name:       "debug",
		OutputType: "stdout",
		Enabled:    true,
	}
	data, err := h.renderConfigMapData(env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	conf := data["output.conf"]
	if !strings.Contains(conf, "Name stdout") || !strings.Contains(conf, "Match *") {
		t.Errorf("output.conf missing stdout defaults:\n%s", conf)
	}
}

// TestRenderConfigMapDataUnsupportedOutput verifies unknown output types
// render a `# unsupported output_type` comment instead of an [OUTPUT] block.
func TestRenderConfigMapDataUnsupportedOutput(t *testing.T) {
	h := &LoggingHandler{}
	env := loggingOperationEnvelope{
		ClusterID:  uuid.New().String(),
		TargetID:   uuid.New().String(),
		TargetType: "output",
		Name:       "mystery",
		OutputType: "kafka",
		Enabled:    true,
	}
	data, err := h.renderConfigMapData(env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	conf := data["output.conf"]
	if !strings.Contains(conf, "# unsupported output_type kafka") {
		t.Errorf("expected unsupported comment; got:\n%s", conf)
	}
	// Verify there's no real `[OUTPUT]` block opener (only the inert one
	// embedded in the comment line).
	for _, line := range strings.Split(conf, "\n") {
		if strings.TrimSpace(line) == "[OUTPUT]" {
			t.Errorf("did not expect [OUTPUT] block; got:\n%s", conf)
			break
		}
	}
}

// TestRenderConfigMapDataPipelineWithNamespacesAndLabels verifies pipelines
// emit Match comments per namespace plus a [FILTER] modify block when
// labels are set.
func TestRenderConfigMapDataPipelineWithNamespacesAndLabels(t *testing.T) {
	h := &LoggingHandler{}
	env := loggingOperationEnvelope{
		ClusterID:  uuid.New().String(),
		TargetID:   uuid.New().String(),
		TargetType: "pipeline",
		Name:       "team-a",
		Enabled:    true,
		Namespaces: json.RawMessage(`["team-a", "team-a-staging"]`),
		Labels:     json.RawMessage(`{"cluster_name": "prod_west", "env": "production"}`),
		Filters:    json.RawMessage(`[]`),
	}
	data, err := h.renderConfigMapData(env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	conf := data["pipeline.conf"]
	for _, want := range []string{
		"# match kube.team-a.*",
		"# match kube.team-a-staging.*",
		"[FILTER]",
		"Name modify",
		"Add         cluster_name prod_west",
		"Add         env production",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("pipeline.conf missing %q\n--- got ---\n%s", want, conf)
		}
	}
}

// TestRenderConfigMapDataPipelineRejectsInvalidLabel verifies labels with
// characters outside [a-zA-Z0-9_.-] render a warning comment instead of an
// Add line.
func TestRenderConfigMapDataPipelineRejectsInvalidLabel(t *testing.T) {
	h := &LoggingHandler{}
	env := loggingOperationEnvelope{
		ClusterID:  uuid.New().String(),
		TargetID:   uuid.New().String(),
		TargetType: "pipeline",
		Name:       "bad-labels",
		Enabled:    true,
		Namespaces: json.RawMessage(`["app"]`),
		// "bad value" contains a space; "good_key" maps to a clean value.
		Labels:  json.RawMessage(`{"good_key": "fine_value", "bad_key": "bad value"}`),
		Filters: json.RawMessage(`[]`),
	}
	data, err := h.renderConfigMapData(env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	conf := data["pipeline.conf"]
	if !strings.Contains(conf, "# warning: skipped invalid label bad_key") {
		t.Errorf("expected warning for bad_key; got:\n%s", conf)
	}
	if !strings.Contains(conf, "Add         good_key fine_value") {
		t.Errorf("expected good_key Add line; got:\n%s", conf)
	}
	if strings.Contains(conf, "bad value") {
		t.Errorf("bad value should not appear in rendered config; got:\n%s", conf)
	}
}

// TestRenderConfigMapDataPipelineWithFilters verifies declared filter
// entries render their own [FILTER] blocks with sorted params.
func TestRenderConfigMapDataPipelineWithFilters(t *testing.T) {
	h := &LoggingHandler{}
	env := loggingOperationEnvelope{
		ClusterID:  uuid.New().String(),
		TargetID:   uuid.New().String(),
		TargetType: "pipeline",
		Name:       "grep-pipeline",
		Enabled:    true,
		Namespaces: json.RawMessage(`["app"]`),
		Labels:     json.RawMessage(`{}`),
		Filters:    json.RawMessage(`[{"type": "grep", "params": {"Regex": "log_level_INFO", "Exclude": "log_level_DEBUG"}}]`),
	}
	data, err := h.renderConfigMapData(env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	conf := data["pipeline.conf"]
	for _, want := range []string{"[FILTER]", "Name grep", "Match kube.app.*", "Exclude log_level_DEBUG", "Regex log_level_INFO"} {
		if !strings.Contains(conf, want) {
			t.Errorf("pipeline.conf missing %q\n--- got ---\n%s", want, conf)
		}
	}
}

// TestRenderConfigMapDataMetaJSONShape verifies meta.json has the
// documented shape: id, name, cluster_id, output_type, generated_at.
func TestRenderConfigMapDataMetaJSONShape(t *testing.T) {
	h := &LoggingHandler{}
	clusterID := uuid.New().String()
	targetID := uuid.New().String()
	env := loggingOperationEnvelope{
		ClusterID:  clusterID,
		TargetID:   targetID,
		TargetType: "output",
		Name:       "primary",
		OutputType: "stdout",
		Enabled:    true,
	}
	data, err := h.renderConfigMapData(env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(data["meta.json"]), &meta); err != nil {
		t.Fatalf("meta.json unmarshal: %v", err)
	}
	for _, key := range []string{"id", "name", "cluster_id", "output_type", "generated_at"} {
		if _, ok := meta[key]; !ok {
			t.Errorf("meta.json missing key %q; keys=%v", key, mapKeys(meta))
		}
	}
	if meta["id"] != targetID {
		t.Errorf("meta.id = %v, want %v", meta["id"], targetID)
	}
	if meta["cluster_id"] != clusterID {
		t.Errorf("meta.cluster_id = %v, want %v", meta["cluster_id"], clusterID)
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// toAny upcasts a map[string]string into a map[string]any so it can share
// the keysOf helper defined in the operation test file.
func toAny(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
