package delivery

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Keep optional observations within the delivery envelope without losing totals.
func boundSystemComponents(components []protocol.SystemComponent) []protocol.SystemComponent {
	for i := range components {
		component := &components[i]
		notes := ""
		sort.Slice(component.Resources, func(a, b int) bool {
			x, y := component.Resources[a], component.Resources[b]
			return x.Group+"/"+x.Plural+"/"+x.Namespace+"/"+x.Name < y.Group+"/"+y.Plural+"/"+y.Namespace+"/"+y.Name
		})
		if count := len(component.Resources); count > 128 {
			component.Resources = component.Resources[:128]
			notes += fmt.Sprintf(" Showing 128 of %d resource observations.", count)
		}
		if count := len(component.Images); count > 32 {
			sort.Strings(component.Images)
			component.Images = component.Images[:32]
			notes += fmt.Sprintf(" Showing 32 of %d images.", count)
		}
		if count := len(component.Volumes); count > 128 {
			sort.Slice(component.Volumes, func(a, b int) bool {
				return component.Volumes[a].Namespace+"/"+component.Volumes[a].Name < component.Volumes[b].Namespace+"/"+component.Volumes[b].Name
			})
			component.Volumes = component.Volumes[:128]
			notes += fmt.Sprintf(" Showing 128 of %d volumes.", count)
		}
		component.Detail = inventoryText(component.Detail, 512-len(notes)) + notes
		for j := range component.Resources {
			component.Resources[j].Detail = inventoryText(component.Resources[j].Detail, 512)
		}
		// Optional observations must not invalidate the primary delivery state.
		// Report unavailable observation explicitly; never invent healthy data.
		if err := (protocol.DeliveryControllerInventory{SystemComponents: []protocol.SystemComponent{*component}}).Validate(); err != nil {
			*component = protocol.SystemComponent{ID: fmt.Sprintf("unavailable-observation-%d", i), Name: "Inventory observation unavailable", Health: "unknown", Detail: "An optional system-component observation failed protocol validation and was excluded. Delivery reconciliation is unaffected."}
		}
	}
	return components
}

func inventoryText(value string, limit int) string {
	value = strings.NewReplacer("\r", " ", "\n", " ", "\x00", " ").Replace(value)
	if len(value) <= limit {
		return value
	}
	value = value[:limit-3]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "..."
}
