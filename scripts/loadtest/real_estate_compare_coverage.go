package main

// Validate the exact public workload catalog, without inferring fixture identities
// omitted from the report. Detail UUIDs must be distinct; their manifest binding
// still relies on the producer's declared fixture hash.
func estateCatalogComplete(r estateReport, p estateMeasuredPhase) bool {
	expected := map[string]bool{}
	members := map[string]bool{}
	for _, v := range r.StartVerification {
		members[v.Member] = true
		switch p.Spec.Mode {
		case "resources":
			for _, kind := range []string{"pods", "deployments", "services"} {
				expected[v.Member+"/namespace_"+kind+"/"] = true
			}
		case "delivery":
			expected[v.Member+"/delivery_inventory/"] = true
			expected[v.Member+"/delivery_list/"] = true
		}
	}
	switch p.Spec.Mode {
	case "idle":
		return len(p.HTTP) == 0
	case "search":
		if r.Search == nil {
			return false
		}
		for _, kind := range r.Search.Types {
			expected["/search_"+kind+"/"] = true
		}
	case "resources", "delivery":
	default:
		return false
	}
	details := map[string]map[string]bool{}
	for _, h := range p.HTTP {
		if p.Spec.Mode == "delivery" && h.Scenario == "delivery_detail" {
			if !members[h.Member] || !validEstateUUID(h.Assignment) {
				return false
			}
			if details[h.Member] == nil {
				details[h.Member] = map[string]bool{}
			}
			if details[h.Member][h.Assignment] {
				return false
			}
			details[h.Member][h.Assignment] = true
			continue
		}
		key := h.Member + "/" + h.Scenario + "/" + h.Assignment
		if !expected[key] {
			return false
		}
		delete(expected, key)
	}
	if len(expected) != 0 {
		return false
	}
	if p.Spec.Mode == "delivery" {
		for name := range members {
			if len(details[name]) != r.Tier {
				return false
			}
		}
	}
	return true
}
