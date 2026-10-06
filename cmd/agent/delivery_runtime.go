package main

import (
	"github.com/alphabravocompany/astronomer-go/internal/agent"
	agentdelivery "github.com/alphabravocompany/astronomer-go/internal/agent/delivery"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func registerDeliveryRuntime(tunnel *agent.TunnelClient, runtime *agentdelivery.Runtime) {
	tunnel.SetObservationRetry(runtime.RetryObservation)
	tunnel.RegisterHandler(protocol.MsgDeliveryStateResponse, runtime.HandleStateResponse)
	tunnel.RegisterHandler(protocol.MsgDeliveryReconcile, runtime.HandleReconcile)
}
