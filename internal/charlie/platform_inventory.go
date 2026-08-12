package charlie

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/version"
)

const (
	maxInventoryPlatforms = 16
	maxObservedVersion    = 128
)

// PlatformAssertion is the local DTO mapped onto the generated bridge type.
type PlatformAssertion struct {
	Pack            string
	PackVersion     string
	ObservedVersion string
	Variant         string
}

// PlatformInventoryProvider reports verified management-plane platform facts.
type PlatformInventoryProvider interface {
	Platforms(context.Context) ([]PlatformAssertion, error)
}

// PostgresVersionNum is a constant safe query over the existing management pool.
type PostgresVersionNum func(context.Context) (string, error)

// ValkeyServerInfo is the existing allowlisted INFO reader.
type ValkeyServerInfo func(context.Context) (string, error)

type kubernetesVersionSource interface {
	ServerVersion() (*version.Info, error)
}

// ManagementPlatformInventory reads only existing product-owned sources.
type ManagementPlatformInventory struct {
	Discovery kubernetesVersionSource
	Postgres  PostgresVersionNum
	Valkey    ValkeyServerInfo
}

func NewManagementPlatformInventory(discoveryClient kubernetesVersionSource, postgres PostgresVersionNum, valkey ValkeyServerInfo) *ManagementPlatformInventory {
	return &ManagementPlatformInventory{Discovery: discoveryClient, Postgres: postgres, Valkey: valkey}
}

func (p *ManagementPlatformInventory) Platforms(ctx context.Context) ([]PlatformAssertion, error) {
	if p == nil {
		return []PlatformAssertion{}, nil
	}
	out := make([]PlatformAssertion, 0, 4)
	var first error
	if assertion, err := p.kubernetes(); err != nil {
		first = err
	} else if assertion != nil {
		out = append(out, *assertion)
	}
	if assertion, err := p.postgresql(ctx); err != nil && first == nil {
		first = err
	} else if assertion != nil {
		out = append(out, *assertion)
	}
	if assertion, err := p.valkey(ctx); err != nil && first == nil {
		first = err
	} else if assertion != nil {
		out = append(out, *assertion)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pack < out[j].Pack })
	out = uniquePacks(out)
	if len(out) > maxInventoryPlatforms {
		out = out[:maxInventoryPlatforms]
	}
	return out, first
}

func uniquePacks(in []PlatformAssertion) []PlatformAssertion {
	seen := map[string]bool{}
	out := make([]PlatformAssertion, 0, len(in))
	for _, item := range in {
		if seen[item.Pack] {
			continue
		}
		seen[item.Pack] = true
		out = append(out, item)
	}
	return out
}

var kubernetesVersionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)(?:\.|$)`)

func (p *ManagementPlatformInventory) kubernetes() (*PlatformAssertion, error) {
	if p.Discovery == nil {
		return nil, nil
	}
	info, err := p.Discovery.ServerVersion()
	if err != nil || info == nil {
		return nil, err
	}
	observed := strings.TrimSpace(info.GitVersion)
	if observed == "" {
		return nil, nil
	}
	if len(observed) > maxObservedVersion {
		observed = observed[:maxObservedVersion]
	}
	match := kubernetesVersionPattern.FindStringSubmatch(observed)
	if match == nil {
		return nil, nil
	}
	line := match[1] + "." + match[2]
	if line != "1.33" && line != "1.34" && line != "1.35" && line != "1.36" {
		return nil, nil
	}
	assertion := &PlatformAssertion{Pack: "kubernetes", PackVersion: line, ObservedVersion: observed}
	switch kubernetesDistribution(observed) {
	case "k3s", "eks", "gke", "aks", "openshift":
		assertion.Variant = kubernetesDistribution(observed)
	}
	return assertion, nil
}

func (p *ManagementPlatformInventory) postgresql(ctx context.Context) (*PlatformAssertion, error) {
	if p.Postgres == nil {
		return nil, nil
	}
	raw, err := p.Postgres(ctx)
	if err != nil {
		return nil, err
	}
	num, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || num < 10000 {
		return nil, nil
	}
	major := num / 10000
	if major != 16 && major != 17 {
		return nil, nil
	}
	return &PlatformAssertion{Pack: "postgresql", PackVersion: strconv.Itoa(major), ObservedVersion: strings.TrimSpace(raw)}, nil
}

func (p *ManagementPlatformInventory) valkey(ctx context.Context) (*PlatformAssertion, error) {
	if p.Valkey == nil {
		return nil, nil
	}
	info, err := p.Valkey(ctx)
	if err != nil {
		return nil, err
	}
	fields := map[string]string{}
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if ok {
			fields[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
		}
	}
	identity := strings.ToLower(fields["server_name"] + " " + fields["valkey_version"] + " " + fields["executable"])
	if !strings.Contains(identity, "valkey") && fields["valkey_version"] == "" {
		return nil, nil
	}
	version := fields["valkey_version"]
	if version == "" {
		return nil, nil
	}
	major := strings.Split(version, ".")[0]
	if major != "8" {
		return nil, nil
	}
	observed := version
	if len(observed) > maxObservedVersion {
		observed = observed[:maxObservedVersion]
	}
	return &PlatformAssertion{Pack: "valkey", PackVersion: "8", ObservedVersion: observed}, nil
}

func collectPlatforms(ctx context.Context, provider PlatformInventoryProvider) []PlatformAssertion {
	if provider == nil {
		return []PlatformAssertion{}
	}
	got, _ := provider.Platforms(ctx)
	if got == nil {
		return []PlatformAssertion{}
	}
	return got
}


