package status

import (
	"encoding/json"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// The wire permits omitted inventory collections while controllers are starting.
// Persist the corresponding empty object/arrays, never JSON null.
func inventoryJSON(inventory protocol.DeliveryControllerInventory) ([]byte, []byte, []byte) {
	if inventory.Components == nil {
		inventory.Components = map[string]string{}
	}
	if inventory.SystemComponents == nil {
		inventory.SystemComponents = []protocol.SystemComponent{}
	}
	if inventory.APIVersions == nil {
		inventory.APIVersions = []string{}
	}
	components, _ := json.Marshal(inventory.Components)
	systemComponents, _ := json.Marshal(inventory.SystemComponents)
	apiVersions, _ := json.Marshal(inventory.APIVersions)
	return components, systemComponents, apiVersions
}
