package agentcompat

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestEvaluate(t *testing.T) {
	cases := map[string]struct {
		status  string
		blocked bool
	}{
		"v1.2.3": {"supported", false},
		"1.2.3":  {"supported", false},
		"v1.1.0": {"supported", false},
		"v1.0.0": {"deprecated", false},
		"v0.9.9": {"blocked", true},
		"v2.0.0": {"blocked", true},
		"":       {"unknown", true},
		"latest": {"unknown", true},
		"dev":    {"unknown", true},
	}
	for version, want := range cases {
		got := Evaluate(version)
		if got.Status != want.status || got.Blocked != want.blocked {
			t.Fatalf("Evaluate(%q) = status:%q blocked:%v, want status:%q blocked:%v", version, got.Status, got.Blocked, want.status, want.blocked)
		}
	}
}

func TestEvaluateConnectNegotiatesIndependentSurfaces(t *testing.T) {
	valid := protocol.ConnectPayload{
		AgentVersion: "v1.1.0", TunnelProtocolVersion: protocol.TunnelProtocolVersion,
		HeartbeatSchemaVersion:  protocol.HeartbeatSchemaVersion,
		DeliveryProtocolVersion: protocol.DeliveryProtocolVersion,
		Capabilities:            protocol.RequiredConnectCapabilities(),
	}
	if got := EvaluateConnect(valid); got.Blocked {
		t.Fatalf("valid connect blocked: %+v", got)
	}
	tests := []struct {
		name string
		edit func(*protocol.ConnectPayload)
		code string
	}{
		{"future agent", func(p *protocol.ConnectPayload) { p.AgentVersion = "v2.0.0" }, "agent_version_future_major"},
		{"tunnel", func(p *protocol.ConnectPayload) { p.TunnelProtocolVersion++ }, "tunnel_protocol_unsupported"},
		{"heartbeat", func(p *protocol.ConnectPayload) { p.HeartbeatSchemaVersion++ }, "heartbeat_schema_unsupported"},
		{"delivery", func(p *protocol.ConnectPayload) { p.DeliveryProtocolVersion = "3.0" }, "delivery_protocol_unsupported"},
		{"capability", func(p *protocol.ConnectPayload) { p.Capabilities = p.Capabilities[:2] }, "required_capability_missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := valid
			payload.Capabilities = append([]string(nil), valid.Capabilities...)
			test.edit(&payload)
			got := EvaluateConnect(payload)
			if !got.Blocked || got.Code != test.code {
				t.Fatalf("EvaluateConnect() = %+v, want blocked code %s", got, test.code)
			}
		})
	}
}
