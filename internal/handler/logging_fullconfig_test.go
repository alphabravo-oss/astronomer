package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// renderFullFluentbitConfig must assemble the real config from the cluster's
// enabled outputs (it previously returned a hardcoded stub).
func TestRenderFullFluentbitConfig(t *testing.T) {
	q := newLoggingFakeQuerier()
	cluster := uuid.New()
	cpg := pgtype.UUID{Bytes: cluster, Valid: true}
	esCfg, _ := json.Marshal(map[string]any{"host": "es.example.com", "port": "9200"})
	splunkCfg, _ := json.Marshal(map[string]any{"hec_url": "https://splunk.example.com:8088", "token": "tok"})

	enabled := uuid.New()
	q.outputs[enabled] = sqlc.LoggingOutput{ID: enabled, Name: "es-out", OutputType: "elasticsearch", Configuration: esCfg, ClusterID: cpg, Enabled: true}
	sp := uuid.New()
	q.outputs[sp] = sqlc.LoggingOutput{ID: sp, Name: "splunk-out", OutputType: "splunk", Configuration: splunkCfg, ClusterID: cpg, Enabled: true}
	off := uuid.New()
	q.outputs[off] = sqlc.LoggingOutput{ID: off, Name: "disabled-out", OutputType: "loki", Configuration: esCfg, ClusterID: cpg, Enabled: false}

	h := NewLoggingHandler(q)
	cfg := h.renderFullFluentbitConfig(context.Background(), cluster)

	for _, want := range []string{"[SERVICE]", "Name tail", "Name kubernetes", "Name es", "Name splunk"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config missing %q:\n%s", want, cfg)
		}
	}
	if strings.Contains(cfg, "Name loki") {
		t.Errorf("disabled output should not be rendered:\n%s", cfg)
	}
	// With outputs present, the stdout fallback should not appear.
	if strings.Contains(cfg, "Name stdout") {
		t.Errorf("stdout fallback should be absent when outputs exist:\n%s", cfg)
	}
}

// With no outputs, fall back to a valid stdout config (not empty).
func TestRenderFullFluentbitConfigNoOutputs(t *testing.T) {
	q := newLoggingFakeQuerier()
	h := NewLoggingHandler(q)
	cfg := h.renderFullFluentbitConfig(context.Background(), uuid.New())
	if !strings.Contains(cfg, "Name stdout") {
		t.Errorf("expected stdout fallback when no outputs:\n%s", cfg)
	}
}
