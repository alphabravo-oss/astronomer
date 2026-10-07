package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type discoverySnapshot struct {
	platformScope bool
	version       string
	versionError  error
	apis          map[string]error
	groups        []string
	groupsError   error
	observation   protocol.DeliveryObservation
}

// Modern refreshes use the REST transport directly so caller cancellation
// applies to every fixed discovery endpoint. No goroutine wraps blocking I/O.
func (p *ClusterProbe) readDiscovery(ctx context.Context, modern bool) discoverySnapshot {
	result := discoverySnapshot{platformScope: p.platformScope, apis: map[string]error{}}
	var info *version.Info
	rest := p.discovery.RESTClient()
	if modern && rest == nil {
		err := errors.New("context-aware discovery transport unavailable")
		result.versionError = err
		result.groupsError = err
		result.observation = errorObservation(err)
		for _, api := range expectedFluxAPIs {
			result.apis[api] = err
		}
		return result
	}
	if modern && rest != nil {
		info = &version.Info{}
		raw, err := rest.Get().AbsPath("/version").Do(ctx).Raw()
		result.versionError = err
		if err == nil {
			result.versionError = json.Unmarshal(raw, info)
		}
	} else {
		info, result.versionError = p.discovery.ServerVersion()
	}
	if info != nil {
		result.version = info.GitVersion
	}
	for _, api := range expectedFluxAPIs {
		if modern && rest != nil {
			result.apis[api] = rest.Get().AbsPath("/apis/" + api).Do(ctx).Into(&metav1.APIResourceList{})
		} else {
			_, result.apis[api] = p.discovery.ServerResourcesForGroupVersion(api)
		}
	}
	if modern {
		groups := &metav1.APIGroupList{}
		if rest != nil {
			result.groupsError = rest.Get().AbsPath("/apis").Do(ctx).Into(groups)
		} else {
			groups, result.groupsError = p.discovery.ServerGroups()
		}
		if groups != nil {
			for _, group := range groups.Groups {
				for _, v := range group.Versions {
					result.groups = append(result.groups, v.GroupVersion)
				}
			}
		}
		at := time.Now().UTC()
		if p.observations != nil {
			at = p.observations.now().UTC()
		}
		result.observation = protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: &at}
		if result.versionError != nil {
			result.observation = errorObservation(result.versionError)
		}
	}
	return result
}
