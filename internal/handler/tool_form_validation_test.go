package handler

import "testing"

func TestCuratedToolValuesValidation(t *testing.T) {
	for _, tc := range []struct {
		name, slug, values string
		valid              bool
	}{
		{"istio split values", "istio", "base:\n  defaultRevision: default\nistiod:\n  autoscaleEnabled: true\n  replicaCount: 2\n", true},
		{"istio invalid boolean", "istio", "istiod:\n  autoscaleEnabled: 'true'\n", false},
		{"istio invalid replica count", "istio", "istiod:\n  replicaCount: 0\n", false},
		{"longhorn defaults", "longhorn", "persistence:\n  defaultClass: false\n  defaultClassReplicaCount: 3\n  reclaimPolicy: Retain\n", true},
		{"neuvector defaults", "neuvector", "controller:\n  replicas: 3\ncve:\n  scanner:\n    replicas: 1\nmanager:\n  svc:\n    type: ClusterIP\n", true},
		{"chart-specific advanced value", "longhorn", "longhornManager:\n  nodeSelector:\n    storage: fast\n", true},
		{"boolean string", "longhorn", "persistence:\n  defaultClass: 'false'\n", false},
		{"invalid enum", "longhorn", "persistence:\n  reclaimPolicy: Erase\n", false},
		{"zero replicas", "neuvector", "controller:\n  replicas: 0\n", false},
		{"fractional replicas", "neuvector", "controller:\n  replicas: 1.5\n", false},
		{"wrong object", "neuvector", "controller: enabled\n", false},
		{"duplicate value", "neuvector", "controller:\n  replicas: 1\n  replicas: 2\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateToolFormValues(tc.slug, tc.values); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}
