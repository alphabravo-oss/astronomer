package charlie

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/version"
)

func TestKubernetesNormalization(t *testing.T) {
	t.Parallel()
	inv := &ManagementPlatformInventory{Discovery: versionOnlyDiscovery{info: &version.Info{GitVersion: "v1.36.2+k3s1"}}}
	got, err := inv.Platforms(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Pack != "kubernetes" || got[0].PackVersion != "1.36" || got[0].Variant != "k3s" || got[0].ObservedVersion != "v1.36.2+k3s1" {
		t.Fatalf("got %#v", got)
	}
}

type versionOnlyDiscovery struct {
	info *version.Info
	err  error
}

func (v versionOnlyDiscovery) ServerVersion() (*version.Info, error) { return v.info, v.err }

func TestMalformedKubernetesVersionIsOmitted(t *testing.T) {
	t.Parallel()
	inv := &ManagementPlatformInventory{Discovery: versionOnlyDiscovery{info: &version.Info{GitVersion: "not-a-version"}}}
	got, err := inv.Platforms(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("got %#v err=%v", got, err)
	}
}

func TestUnknownDistributionOmitsVariant(t *testing.T) {
	t.Parallel()
	inv := &ManagementPlatformInventory{Discovery: versionOnlyDiscovery{info: &version.Info{GitVersion: "v1.36.1"}}}
	got, err := inv.Platforms(context.Background())
	if err != nil || len(got) != 1 || got[0].Variant != "" {
		t.Fatalf("got %#v err=%v", got, err)
	}
}

func TestUnsupportedKubernetesLineIsOmitted(t *testing.T) {
	t.Parallel()
	inv := &ManagementPlatformInventory{Discovery: versionOnlyDiscovery{info: &version.Info{GitVersion: "v1.28.0"}}}
	got, err := inv.Platforms(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("got %#v err=%v", got, err)
	}
}

func TestPostgreSQLNormalization(t *testing.T) {
	t.Parallel()
	inv := &ManagementPlatformInventory{Postgres: func(context.Context) (string, error) { return "160003", nil }}
	got, err := inv.Platforms(context.Background())
	if err != nil || len(got) != 1 || got[0].Pack != "postgresql" || got[0].PackVersion != "16" {
		t.Fatalf("got %#v err=%v", got, err)
	}
	inv.Postgres = func(context.Context) (string, error) { return "150000", nil }
	got, err = inv.Platforms(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("unsupported postgres %#v %v", got, err)
	}
}

func TestValkeyRequiresPositiveIdentity(t *testing.T) {
	t.Parallel()
	redisOnly := &ManagementPlatformInventory{Valkey: func(context.Context) (string, error) { return "redis_version:7.4.1\n", nil }}
	got, err := redisOnly.Platforms(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("redis-only should omit: %#v %v", got, err)
	}
	valkey := &ManagementPlatformInventory{Valkey: func(context.Context) (string, error) {
		return "server_name:valkey\nvalkey_version:8.1.1\n", nil
	}}
	got, err = valkey.Platforms(context.Background())
	if err != nil || len(got) != 1 || got[0].Pack != "valkey" || got[0].PackVersion != "8" {
		t.Fatalf("valkey %#v %v", got, err)
	}
}

func TestInventoryPartialFailureIsNonfatal(t *testing.T) {
	t.Parallel()
	inv := &ManagementPlatformInventory{
		Discovery: versionOnlyDiscovery{err: errors.New("discovery down")},
		Valkey:    func(context.Context) (string, error) { return "server_name:valkey\nvalkey_version:8.0.0\n", nil },
	}
	got, err := inv.Platforms(context.Background())
	if err == nil {
		t.Fatal("expected discovery error to be reported")
	}
	if len(got) != 1 || got[0].Pack != "valkey" {
		t.Fatalf("partial inventory %#v", got)
	}
}

func TestInventoryIsDeterministicallySorted(t *testing.T) {
	t.Parallel()
	inv := &ManagementPlatformInventory{
		Discovery: versionOnlyDiscovery{info: &version.Info{GitVersion: "v1.36.0"}},
		Postgres:  func(context.Context) (string, error) { return "170000", nil },
		Valkey:    func(context.Context) (string, error) { return "server_name:valkey\nvalkey_version:8.0.1\n", nil },
	}
	got, err := inv.Platforms(context.Background())
	if err != nil || len(got) != 3 {
		t.Fatalf("got %#v err=%v", got, err)
	}
	if got[0].Pack != "kubernetes" || got[1].Pack != "postgresql" || got[2].Pack != "valkey" {
		t.Fatalf("order %#v", got)
	}
}

func TestInventoryNeverCallsDownstream(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("platform_inventory.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, forbidden := range []string{"downstream", "CoreV1()", "Pods(", "kubectl"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("inventory mentions %q", forbidden)
		}
	}
}
