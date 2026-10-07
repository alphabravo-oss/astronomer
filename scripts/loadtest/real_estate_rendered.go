package main

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

var estateResourceName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)

func estateResourcePath(ref astroclient.DeliveryResourceIdentity) string {
	plural := ""
	group := ""
	switch ref.ApiVersion + "/" + ref.Kind {
	case "v1/Pod":
		group = "/api/v1"
		plural = "pods"
	case "v1/Service":
		group = "/api/v1"
		plural = "services"
	case "v1/PersistentVolumeClaim":
		group = "/api/v1"
		plural = "persistentvolumeclaims"
	case "apps/v1/Deployment":
		group = "/apis/apps/v1"
		plural = "deployments"
	case "apps/v1/StatefulSet":
		group = "/apis/apps/v1"
		plural = "statefulsets"
	case "apps/v1/DaemonSet":
		group = "/apis/apps/v1"
		plural = "daemonsets"
	default:
		return ""
	}
	if ref.Namespace == nil || !estateName.MatchString(*ref.Namespace) || !estateResourceName.MatchString(ref.Name) {
		return ""
	}
	return group + "/namespaces/" + *ref.Namespace + "/" + plural + "/" + ref.Name
}
func verifyEstateRendered(ctx context.Context, c *http.Client, base, token string, m estateMember, a estateAssignment, inv astroclient.DeliveryResourceInventory, now time.Time) (bool, error) {
	if inv.Observation == nil || inv.Observation.State != "current" || !estateRecent(inv.Observation.ObservedAt, now, 5*time.Minute) || inv.Resources == nil || len(a.RenderedResources) == 0 {
		return false, nil
	}
	refs := *inv.Resources
	// Truncated inventories cannot establish all rendered namespace membership.
	if inv.Entries != len(refs) || len(refs) != len(a.RenderedResources) || len(refs) > 64 {
		return false, nil
	}
	want := map[string]bool{}
	for _, ref := range a.RenderedResources {
		key := estateResourcePath(ref)
		if key == "" || want[key] {
			return false, errors.New("invalid or duplicate declared rendered reference")
		}
		want[key] = true
	}
	for _, ref := range refs {
		path := estateResourcePath(ref)
		if path == "" {
			return false, nil
		}
		if ref.Namespace == nil || *ref.Namespace != m.Namespace || !want[path] {
			return false, errors.New("rendered inventory differs from declared namespace fixtures")
		}
		delete(want, path)
		var object struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			Metadata   struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
				UID       string `json:"uid"`
			} `json:"metadata"`
		}
		if err := estateGET(ctx, c, base+"/api/v1/clusters/"+m.ClusterID+"/k8s"+path, token, &object, false); err != nil {
			return false, err
		}
		if object.APIVersion != ref.ApiVersion || object.Kind != ref.Kind || object.Metadata.Name != ref.Name || object.Metadata.Namespace != m.Namespace || object.Metadata.UID == "" {
			return false, errors.New("rendered resource GET identity mismatch")
		}
	}
	return true, nil
}
