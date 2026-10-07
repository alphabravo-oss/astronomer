package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/alphabravocompany/astronomer-go/internal/agent/kuberequests"
	"github.com/alphabravocompany/astronomer-go/internal/agent/observation"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type sharedProbe struct {
	source        observation.Source
	discovery     refreshSlot[discoverySnapshot]
	dynamic       refreshSlot[dynamicSnapshot]
	now           func() time.Time
	readDiscovery func(context.Context) discoverySnapshot
}

func (p *ClusterProbe) WithObservationSource(source observation.Source) *ClusterProbe {
	if p != nil && source != nil {
		p.observations = &sharedProbe{source: source, now: time.Now, readDiscovery: func(ctx context.Context) discoverySnapshot { return p.readDiscovery(ctx, true) }}
	}
	return p
}
func (p *ClusterProbe) InspectObserved(ctx context.Context) (protocol.DeliveryControllerInventory, Capabilities, error) {
	ctx = kuberequests.WithConsumer(ctx, kuberequests.DeliveryInventory)
	if p.observations == nil {
		return protocol.DeliveryControllerInventory{}, Capabilities{}, fmt.Errorf("shared observation source is required")
	}
	shared := p.observations
	key := controllerRevision(shared.source.ObservationSnapshot(observation.Deployments), shared.source)
	discovery, err := shared.discovery.get(ctx, key, shared.now, func(ctx context.Context) (discoverySnapshot, time.Duration) {
		start := time.Now()
		result := shared.readDiscovery(ctx)
		good := result.versionError == nil && result.groupsError == nil
		for _, err := range result.apis {
			if err != nil && !apierrors.IsNotFound(err) {
				good = false
			}
		}
		recordRefresh("discovery", start, good)
		return result, 2 * time.Minute
	})
	if err != nil {
		return protocol.DeliveryControllerInventory{}, Capabilities{}, err
	}
	var dynamic dynamicSnapshot
	if p.platformScope {
		dynamic, err = shared.dynamic.get(ctx, key, shared.now, func(ctx context.Context) (dynamicSnapshot, time.Duration) {
			start := time.Now()
			result, interval := p.readDynamicInventory(ctx, shared.now())
			good := true
			for _, component := range result.components {
				if component.Observation.State != protocol.ObservationCurrent && component.Observation.State != protocol.ObservationAbsent {
					good = false
				}
			}
			recordRefresh("dynamic", start, good)
			return result, interval
		})
		if err != nil {
			return protocol.DeliveryControllerInventory{}, Capabilities{}, err
		}
	}
	// Sample typed stores after bounded I/O so watch revocation during a refresh
	// cannot be hidden by a snapshot captured before the request.
	snapshots := make(map[observation.Kind]observation.Snapshot)
	for _, kind := range observation.Kinds() {
		snapshot := shared.source.ObservationSnapshot(kind)
		snapshot.Observation = ageObservation(snapshot.Observation, shared.now())
		recordSource(string(kind), snapshot.Observation, shared.now())
		snapshots[kind] = snapshot
	}
	deployments := snapshots[observation.Deployments]
	inventory, capabilities, err := evaluateControllers(discovery, func(name string) (*appsv1.Deployment, error) {
		for _, item := range deployments.Deployments {
			if item.Namespace == DeliverySystemNamespace && item.Name == name {
				return item, nil
			}
		}
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: "apps", Resource: "deployments"}, name)
	})
	if err != nil {
		return inventory, capabilities, err
	}
	observed := combineObservations(deployments.Observation, discovery.observation)
	for _, api := range expectedFluxAPIs {
		apiErr := discovery.apis[api]
		if apiErr != nil && !apierrors.IsNotFound(apiErr) {
			observed = combineObservations(observed, errorObservation(apiErr))
		}
	}
	inventory.Observation = &observed
	recordSource("controllers", observed, shared.now())
	if observed.State != protocol.ObservationCurrent {
		inventory.Ready = false
		inventory.CompatibilityMessage = "controller_inventory_unavailable"
		capabilities.NamespaceScope = false
		capabilities.PlatformScope = false
	}
	if p.platformScope {
		inventory.SystemComponents = sharedSystemComponents(snapshots, discovery, dynamic, shared.now())
	}

	return inventory, capabilities, nil
}

func controllerRevision(snapshot observation.Snapshot, source observation.Source) string {
	items := []string{}
	for _, item := range snapshot.Deployments {
		if item.Namespace == DeliverySystemNamespace {
			encoded, _ := json.Marshal(struct {
				UID        string
				Generation int64
				Images     []string
				Version    string
			}{string(item.UID), item.Generation, controllerImages(item), item.Labels["app.kubernetes.io/version"]})
			items = append(items, item.Name+string(encoded))
		}
	}
	sort.Strings(items)
	revision := uint64(0)
	if source, ok := source.(interface{ DiscoveryRevision() uint64 }); ok {
		revision = source.DiscoveryRevision()
	}
	return fmt.Sprintf("%x/%d", sha256.Sum256([]byte(fmt.Sprint(items))), revision)
}
func controllerImages(item *appsv1.Deployment) []string {
	images := make([]string, 0, len(item.Spec.Template.Spec.Containers))
	for _, container := range item.Spec.Template.Spec.Containers {
		images = append(images, container.Name+"="+container.Image)
	}
	return images
}
func errorObservation(err error) protocol.DeliveryObservation {
	state := protocol.ObservationUnavailable
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
		state = protocol.ObservationDenied
	}
	if err == context.Canceled {
		state = protocol.ObservationDisconnected
	}
	return protocol.DeliveryObservation{State: state}
}
func combineObservations(a, b protocol.DeliveryObservation) protocol.DeliveryObservation {
	if a.State != protocol.ObservationCurrent {
		return a
	}
	if b.State != protocol.ObservationCurrent {
		return b
	}
	if a.ObservedAt == nil || b.ObservedAt == nil {
		return protocol.DeliveryObservation{State: protocol.ObservationUnsynced}
	}
	if b.ObservedAt.Before(*a.ObservedAt) {
		a.ObservedAt = b.ObservedAt
	}
	at := *a.ObservedAt
	a.ObservedAt = &at
	return a
}

func ageObservation(source protocol.DeliveryObservation, now time.Time) protocol.DeliveryObservation {
	if source.State == "" {
		source.State = protocol.ObservationUnsynced
	}
	if source.ObservedAt != nil && (source.ObservedAt.IsZero() || source.ObservedAt.After(now.Add(protocol.MaxObservationClockSkew))) {
		return protocol.DeliveryObservation{State: protocol.ObservationUnavailable}
	}
	if source.State == protocol.ObservationCurrent {
		if source.ObservedAt == nil {
			return protocol.DeliveryObservation{State: protocol.ObservationUnsynced}
		}
		if now.Sub(*source.ObservedAt) > 4*time.Minute {
			source.State = protocol.ObservationStale
		}
	}
	return source
}
