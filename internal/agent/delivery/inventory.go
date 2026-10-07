package delivery

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"k8s.io/apimachinery/pkg/api/validate/content"
	"k8s.io/apimachinery/pkg/util/validation"
)

var inventoryKindPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

// fluxResourceIdentity decodes the ResourceRef shared by Flux Kustomization
// and HelmRelease inventories. Invalid references are omitted without changing
// the total entry count or deriving individual resource health.
func fluxResourceIdentity(raw any) (protocol.DeliveryResourceIdentity, bool) {
	entry, ok := raw.(map[string]any)
	if !ok {
		return protocol.DeliveryResourceIdentity{}, false
	}
	id, idOK := entry["id"].(string)
	version, versionOK := entry["v"].(string)
	parts := strings.Split(id, "_")
	if !idOK || !versionOK || len(parts) != 4 || len(validation.IsDNS1035Label(version)) != 0 {
		return protocol.DeliveryResourceIdentity{}, false
	}
	namespace, name, group, kind := parts[0], parts[1], parts[2], parts[3]
	if (namespace != "" && len(validation.IsDNS1123Label(namespace)) != 0) ||
		name == "" || len(name) > 253 || len(content.IsPathSegmentName(name)) != 0 ||
		strings.IndexFunc(name, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 ||
		(group != "" && len(validation.IsDNS1123Subdomain(group)) != 0) ||
		len(kind) > 128 || !inventoryKindPattern.MatchString(kind) {
		return protocol.DeliveryResourceIdentity{}, false
	}
	apiVersion := version
	if group != "" {
		apiVersion = group + "/" + version
	}
	if len(apiVersion) > 253 {
		return protocol.DeliveryResourceIdentity{}, false
	}
	return protocol.DeliveryResourceIdentity{APIVersion: apiVersion, Kind: kind, Namespace: namespace, Name: name}, true
}
