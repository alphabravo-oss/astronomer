// Package mirror ingests the observed state managed-cluster agents stream
// over the tunnel (MsgMirrorEvent): IngressClass, GatewayClass,
// NetworkPolicy, ResourceQuota and LimitRange rows in the mirrored_* tables,
// plus trivy-operator VulnerabilityReports routed to the scanner ingester.
// It is the read side of the operator; the management CRD reconcilers live
// in internal/operator/lifecycle.
package mirror
