package tunnel

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/downstreamboundary"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestSendToAgentInstrumentsEveryDownstreamRequestClass(t *testing.T) {
	cases := []struct {
		message   protocol.MessageType
		operation downstreamboundary.Operation
	}{
		{protocol.MsgK8sRequest, downstreamboundary.OperationKubernetes},
		{protocol.MsgK8sStreamRequest, downstreamboundary.OperationKubernetes},
		{protocol.MsgK8sStreamStop, downstreamboundary.OperationKubernetes},
		{protocol.MsgHelmInstall, downstreamboundary.OperationHelm},
		{protocol.MsgHelmUpgrade, downstreamboundary.OperationHelm},
		{protocol.MsgHelmUninstall, downstreamboundary.OperationHelm},
		{protocol.MsgHelmRollback, downstreamboundary.OperationHelm},
		{protocol.MsgHelmStatus, downstreamboundary.OperationHelm},
		{protocol.MsgHelmHistory, downstreamboundary.OperationHelm},
		{protocol.MsgExecStart, downstreamboundary.OperationExec},
		{protocol.MsgExecInput, downstreamboundary.OperationExec},
		{protocol.MsgExecResize, downstreamboundary.OperationExec},
		{protocol.MsgExecEnd, downstreamboundary.OperationExec},
		{protocol.MsgLogStart, downstreamboundary.OperationLogs},
		{protocol.MsgLogStop, downstreamboundary.OperationLogs},
		{protocol.MsgServiceProxyRequest, downstreamboundary.OperationServiceProxy},
		{protocol.MsgProxyRequest, downstreamboundary.OperationServiceProxy},
		{protocol.MsgRBACSyncRequest, downstreamboundary.OperationRBAC},
		{protocol.MsgDecommission, downstreamboundary.OperationAgentCommand},
		{protocol.MsgAgentUpgrade, downstreamboundary.OperationAgentCommand},
		{protocol.MsgDesiredStateRequest, downstreamboundary.OperationAgentCommand},
	}
	hub := NewHub(nil)
	for _, test := range cases {
		t.Run(string(test.message), func(t *testing.T) {
			operation, ok := downstreamOperation(&protocol.Message{Type: test.message})
			if !ok || operation != test.operation {
				t.Fatalf("message %s classified as %s ok=%v", test.message, operation, ok)
			}
			before := downstreamboundary.TakeSnapshot()
			if err := hub.SendToAgent("not-connected", &protocol.Message{Type: test.message}); err == nil {
				t.Fatal("test unexpectedly found a downstream agent")
			}
			if delta := downstreamboundary.TakeSnapshot().DeltaTotal(before); delta != 1 {
				t.Fatalf("boundary delta=%d want=1", delta)
			}
		})
	}
}
