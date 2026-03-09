package agent

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func testConfig() *AgentConfig {
	return &AgentConfig{
		ServerURL:         "wss://test.example.com",
		ClusterID:         "test-cluster",
		AgentToken:        "test-token",
		AgentID:           "agent-1",
		ReconnectBackoff:  5,
		MaxReconnect:      300,
		HeartbeatInterval: 30,
		MetricsInterval:   60,
		HealthAddr:        ":8081",
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestNewTunnelClient(t *testing.T) {
	cfg := testConfig()
	log := testLogger()

	tc := NewTunnelClient(cfg, log)

	if tc == nil {
		t.Fatal("NewTunnelClient returned nil")
	}
	if tc.config != cfg {
		t.Error("config not set correctly")
	}
	if tc.handlers == nil {
		t.Error("handlers map not initialized")
	}
	if tc.sendCh == nil {
		t.Error("send channel not initialized")
	}
	if tc.IsConnected() {
		t.Error("new client should not be connected")
	}
}

func TestBackoffDuration(t *testing.T) {
	tests := []struct {
		attempt     int
		base        int
		max         int
		wantSeconds float64
	}{
		{attempt: 0, base: 5, max: 300, wantSeconds: 5},
		{attempt: 1, base: 5, max: 300, wantSeconds: 10},
		{attempt: 2, base: 5, max: 300, wantSeconds: 20},
		{attempt: 3, base: 5, max: 300, wantSeconds: 40},
		{attempt: 4, base: 5, max: 300, wantSeconds: 80},
		{attempt: 5, base: 5, max: 300, wantSeconds: 160},
		{attempt: 6, base: 5, max: 300, wantSeconds: 300}, // capped at max
		{attempt: 10, base: 5, max: 300, wantSeconds: 300}, // still capped
		{attempt: 0, base: 1, max: 10, wantSeconds: 1},
		{attempt: 5, base: 1, max: 10, wantSeconds: 10}, // capped
	}

	for _, tt := range tests {
		got := BackoffDuration(tt.attempt, tt.base, tt.max)
		want := time.Duration(tt.wantSeconds) * time.Second
		if got != want {
			t.Errorf("BackoffDuration(%d, %d, %d) = %v, want %v",
				tt.attempt, tt.base, tt.max, got, want)
		}
	}
}

func TestRegisterHandler(t *testing.T) {
	tc := NewTunnelClient(testConfig(), testLogger())

	called := false
	handler := func(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
		called = true
		return nil, nil
	}

	tc.RegisterHandler(protocol.MsgK8sRequest, handler)

	h, ok := tc.handlers[protocol.MsgK8sRequest]
	if !ok {
		t.Fatal("handler not registered")
	}

	_, _ = h(context.Background(), &protocol.Message{})
	if !called {
		t.Error("registered handler was not called")
	}
}

func TestRegisterMultipleHandlers(t *testing.T) {
	tc := NewTunnelClient(testConfig(), testLogger())

	handlerTypes := []protocol.MessageType{
		protocol.MsgK8sRequest,
		protocol.MsgHelmInstall,
		protocol.MsgHelmUpgrade,
		protocol.MsgHelmUninstall,
		protocol.MsgHelmRollback,
		protocol.MsgHelmStatus,
	}

	for _, mt := range handlerTypes {
		tc.RegisterHandler(mt, func(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
			return nil, nil
		})
	}

	for _, mt := range handlerTypes {
		if _, ok := tc.handlers[mt]; !ok {
			t.Errorf("handler for %s not registered", mt)
		}
	}
}

func TestSend(t *testing.T) {
	tc := NewTunnelClient(testConfig(), testLogger())

	msg := &protocol.Message{
		Type:      protocol.MsgHeartbeat,
		Timestamp: time.Now().UTC(),
	}

	err := tc.Send(msg)
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	// Verify message is in the channel.
	select {
	case received := <-tc.sendCh:
		if received.Type != protocol.MsgHeartbeat {
			t.Errorf("received message type = %s, want %s", received.Type, protocol.MsgHeartbeat)
		}
	default:
		t.Error("no message in send channel")
	}
}

func TestSendChannelFull(t *testing.T) {
	cfg := testConfig()
	log := testLogger()
	tc := NewTunnelClient(cfg, log)

	// Fill the channel.
	for i := 0; i < 256; i++ {
		_ = tc.Send(&protocol.Message{Type: protocol.MsgHeartbeat, Timestamp: time.Now().UTC()})
	}

	// Next send should fail.
	err := tc.Send(&protocol.Message{Type: protocol.MsgHeartbeat, Timestamp: time.Now().UTC()})
	if err == nil {
		t.Error("expected error when channel is full, got nil")
	}
}

func TestIsConnected(t *testing.T) {
	tc := NewTunnelClient(testConfig(), testLogger())

	if tc.IsConnected() {
		t.Error("should not be connected initially")
	}

	tc.setConnected(true)
	if !tc.IsConnected() {
		t.Error("should be connected after setConnected(true)")
	}

	tc.setConnected(false)
	if tc.IsConnected() {
		t.Error("should not be connected after setConnected(false)")
	}
}

func TestCloseWithoutConnection(t *testing.T) {
	tc := NewTunnelClient(testConfig(), testLogger())

	err := tc.Close()
	if err != nil {
		t.Errorf("Close on nil connection should not error, got: %v", err)
	}
}
