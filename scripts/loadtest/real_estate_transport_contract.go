package main

import (
	"errors"
	"math"
	"slices"
)

const estateTransportCounter = "astronomer_agent_kubernetes_requests_total"
const estateTransportSentinel = "astronomer_agent_kubernetes_request_instrumentation_info"
const estateProcessStart = "process_start_time_seconds"
const estateTransportSeriesLimit = 4 * 5 * 18 * 7
const estateLegacySeriesLimit = 256

var estateConsumers = []string{"shared_observation", "delivery_inventory", "delivery_assignment_observation", "other"}
var estateOperations = []string{"list", "get", "watch", "discovery", "other"}
var estateResources = []string{"pods", "persistentvolumeclaims", "deployments", "statefulsets", "daemonsets", "storageclasses", "customresourcedefinitions", "gitrepositories", "ocirepositories", "helmrepositories", "kustomizations", "helmreleases", "volumes", "nodes", "certificates", "gateways", "volumesnapshots", "other"}
var estateOutcomes = []string{"1xx", "2xx", "3xx", "4xx", "5xx", "transport_error", "other"}

func estateTransportMetric(name string) bool {
	return name == estateTransportCounter || name == estateTransportSentinel || name == estateProcessStart
}
func estateTransportLabels(name string, labels map[string]string, instance string) error {
	if name == estateProcessStart {
		if len(labels) != 0 {
			return errors.New("unexpected process start labels")
		}
		return nil
	}
	if labels["astronomer_instance_id"] != instance {
		return errors.New("unexpected transport metric producer")
	}
	if name == estateTransportSentinel {
		if len(labels) != 2 || labels["schema"] != "v1" {
			return errors.New("unsupported transport instrumentation schema")
		}
		return nil
	}
	if len(labels) != 5 || !slices.Contains(estateConsumers, labels["consumer"]) || !slices.Contains(estateOperations, labels["operation"]) || !slices.Contains(estateResources, labels["resource"]) || !slices.Contains(estateOutcomes, labels["outcome"]) {
		return errors.New("unexpected transport counter labels")
	}
	return nil
}
func estateTransportValue(p estatePoint) error {
	if p.Name == estateTransportCounter && (math.Trunc(p.Value) != p.Value || p.Value >= 1<<53) {
		return errors.New("transport counter is not an exactly representable integer")
	}
	if p.Name == estateTransportSentinel && p.Value != 1 {
		return errors.New("transport instrumentation sentinel must equal one")
	}
	return nil
}
