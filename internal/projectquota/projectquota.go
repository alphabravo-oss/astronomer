// Package projectquota owns the project-wide resource-cap contract.
//
// Kubernetes ResourceQuota is namespace-scoped. A project can own multiple
// namespaces, so a project cap must be split before it is applied. Keeping
// that arithmetic here ensures the API and reconciler always agree on the
// desired allocation.
package projectquota

import (
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

// Cap is a project-wide cap. Empty CPU/memory and zero Pods mean unbounded
// for that dimension. Values use Kubernetes quantity syntax.
type Cap struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
	Pods   int32  `json:"pods,omitempty"`
}

// Empty reports whether every cap dimension is unbounded.
func (c Cap) Empty() bool {
	return strings.TrimSpace(c.CPU) == "" && strings.TrimSpace(c.Memory) == "" && c.Pods == 0
}

// Validate accepts only positive Kubernetes quantities. Negative pod counts
// are never meaningful; zero means that the pods dimension is unbounded.
func (c Cap) Validate() error {
	if c.Pods < 0 {
		return fmt.Errorf("pods must be zero or positive")
	}
	if err := validateQuantity("cpu", c.CPU); err != nil {
		return err
	}
	return validateQuantity("memory", c.Memory)
}

func validateQuantity(name, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	q, err := resource.ParseQuantity(value)
	if err != nil {
		return fmt.Errorf("invalid %s quantity: %w", name, err)
	}
	if q.Sign() <= 0 {
		return fmt.Errorf("%s must be positive", name)
	}
	return nil
}

// Allocate deterministically divides a cap across namespaces. Namespaces are
// sorted before division, so the same membership has the same allocation
// regardless of database order. Remainders go to lexical prefixes; every
// bounded dimension sums exactly to the project cap.
func Allocate(cap Cap, namespaces []string) (map[string]Cap, error) {
	if err := cap.Validate(); err != nil {
		return nil, err
	}
	names := uniqueSortedNamespaces(namespaces)
	allocations := make(map[string]Cap, len(names))
	if len(names) == 0 {
		return allocations, nil
	}
	// Preserve the operator's canonical spelling when no split is required.
	// It avoids needless SSA churn (for example 4 -> 4000m, 8Gi -> bytes).
	if len(names) == 1 {
		allocations[names[0]] = cap
		return allocations, nil
	}

	cpu, err := allocateQuantity(cap.CPU, len(names), true)
	if err != nil {
		return nil, err
	}
	memory, err := allocateQuantity(cap.Memory, len(names), false)
	if err != nil {
		return nil, err
	}
	if cap.Pods > 0 && int64(cap.Pods) < int64(len(names)) {
		return nil, fmt.Errorf("pods cap is too small to allocate one unit to each of %d namespaces", len(names))
	}
	pods := allocateInt64(int64(cap.Pods), len(names))
	for i, namespace := range names {
		allocations[namespace] = Cap{
			CPU:    cpu[i],
			Memory: memory[i],
			Pods:   int32(pods[i]),
		}
	}
	return allocations, nil
}

func uniqueSortedNamespaces(namespaces []string) []string {
	seen := make(map[string]struct{}, len(namespaces))
	for _, namespace := range namespaces {
		namespace = strings.TrimSpace(namespace)
		if namespace != "" {
			seen[namespace] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for namespace := range seen {
		names = append(names, namespace)
	}
	sort.Strings(names)
	return names
}

func allocateQuantity(value string, count int, cpu bool) ([]string, error) {
	values := make([]string, count)
	value = strings.TrimSpace(value)
	if value == "" {
		return values, nil
	}
	q, err := resource.ParseQuantity(value)
	if err != nil {
		return nil, err
	}
	// MilliValue is the smallest portable CPU unit. For memory, Value is the
	// smallest byte count accepted by ResourceQuota. Validation above ensures
	// both inputs are positive.
	var units int64
	if cpu {
		units = q.MilliValue()
	} else {
		units = q.Value()
	}
	// Empty / zero means unbounded in the public cap model. Never emit a zero
	// share for a bounded dimension: Kubernetes would interpret it as no cap,
	// allowing the project to exceed its total. A cap must be large enough to
	// give every namespace one minimum enforceable unit.
	if units < int64(count) {
		return nil, fmt.Errorf("%s cap is too small to allocate one unit to each of %d namespaces", value, count)
	}
	for i, unit := range allocateInt64(units, count) {
		if unit == 0 {
			continue
		}
		if cpu {
			values[i] = fmt.Sprintf("%dm", unit)
		} else {
			values[i] = fmt.Sprintf("%d", unit)
		}
	}
	return values, nil
}

func allocateInt64(total int64, count int) []int64 {
	values := make([]int64, count)
	if total <= 0 || count == 0 {
		return values
	}
	base, remainder := total/int64(count), total%int64(count)
	for i := range values {
		values[i] = base
		if int64(i) < remainder {
			values[i]++
		}
	}
	return values
}
