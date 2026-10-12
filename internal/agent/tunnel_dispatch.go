package agent

import (
	"context"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"go.opentelemetry.io/otel/codes"
)

func (tc *TunnelClient) dispatchMessage(dispatchCtx context.Context, m *protocol.Message, c dispatchClass, handler MessageHandler) {
	handlerCtx, span := tc.startMessageSpan(dispatchCtx, m)
	if m.Type == protocol.MsgDecommission {
		var owner *decommissionReply
		handlerCtx, owner = withDecommissionReply(handlerCtx)
		defer owner.discardUnlessQueued()
	}
	defer span.End()
	logger := observability.WithTraceID(tc.log, handlerCtx)
	resp, err := handler(handlerCtx, m)
	status := "success"
	synthesized := false
	if err != nil {
		status = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		logger.Error("handler error", "type", m.Type, "error", err)
		resp = handlerErrorReply(m, err)
		synthesized = true
	}
	if resp == nil {
		observability.IncrementWithTraceExemplar(handlerCtx, agentTunnelMessagesTotal.WithLabelValues(observability.MetricValues(string(m.Type), status)...))
		return
	}
	if sendErr := tc.sendHandlerReply(handlerCtx, resp, c, synthesized); sendErr != nil {
		status = "error"
		span.RecordError(sendErr)
		span.SetStatus(codes.Error, sendErr.Error())
		logger.Error("failed to send response", "error", sendErr)
	}
	observability.IncrementWithTraceExemplar(handlerCtx, agentTunnelMessagesTotal.WithLabelValues(observability.MetricValues(string(m.Type), status)...))
}
