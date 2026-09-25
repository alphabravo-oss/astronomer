package fluxdistribution_test

import (
	"bytes"
	"io"
	"os"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

type manifest struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		PodSelector struct {
			MatchLabels map[string]string `yaml:"matchLabels"`
		} `yaml:"podSelector"`
		Egress []struct {
			To    []any `yaml:"to"`
			Ports []struct {
				Port     int    `yaml:"port"`
				Protocol string `yaml:"protocol"`
			} `yaml:"ports"`
		} `yaml:"egress"`
	} `yaml:"spec"`
}

func TestAllowDNSIsPortableAcrossServiceDNATImplementations(t *testing.T) {
	content, err := os.ReadFile("system-resources.yaml")
	if err != nil {
		t.Fatal(err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(content))
	for {
		var resource manifest
		if err := decoder.Decode(&resource); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		if resource.Kind != "NetworkPolicy" || resource.Metadata.Name != "allow-dns" {
			continue
		}

		if got, want := resource.Spec.PodSelector.MatchLabels, map[string]string{"app.kubernetes.io/part-of": "flux"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("allow-dns selects %v, want %v", got, want)
		}
		if len(resource.Spec.Egress) != 1 {
			t.Fatalf("allow-dns has %d egress rules, want 1", len(resource.Spec.Egress))
		}
		rule := resource.Spec.Egress[0]
		if len(rule.To) != 0 {
			t.Fatalf("allow-dns restricts destinations (%v); service-IP DNS must work before or after DNAT", rule.To)
		}
		if len(rule.Ports) != 2 {
			t.Fatalf("allow-dns exposes %d ports, want 2", len(rule.Ports))
		}
		gotPorts := make(map[string]bool, len(rule.Ports))
		for _, port := range rule.Ports {
			gotPorts[port.Protocol+"/53"] = port.Port == 53
		}
		wantPorts := map[string]bool{"TCP/53": true, "UDP/53": true}
		if !reflect.DeepEqual(gotPorts, wantPorts) {
			t.Fatalf("allow-dns ports = %v, want %v", gotPorts, wantPorts)
		}
		return
	}

	t.Fatal("allow-dns NetworkPolicy not found")
}
