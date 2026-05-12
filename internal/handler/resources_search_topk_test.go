package handler

import (
	"math/rand"
	"sort"
	"testing"
)

// Parity test. The bounded top-K heap must produce
// the *same* trimmed output (modulo the now-irrelevant stable ordering of
// equal-key items) as the prior sort.SliceStable + truncate approach, so
// the API contract is unchanged for clients.

func TestSearchTopKHeap_MatchesSortTruncate(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(42))
	const totalItems = 2500
	const k = 100

	// Generate a population of items with varied (cluster, namespace,
	// name) keys to exercise the comparator across all 3 levels.
	clusters := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	namespaces := []string{"default", "kube-system", "monitoring", "apps", "data"}

	items := make([]map[string]any, totalItems)
	for i := 0; i < totalItems; i++ {
		items[i] = map[string]any{
			"cluster_name": clusters[rng.Intn(len(clusters))],
			"namespace":    namespaces[rng.Intn(len(namespaces))],
			"name":         randName(rng),
		}
	}

	// Reference: full sort, then truncate.
	reference := append([]map[string]any(nil), items...)
	sort.SliceStable(reference, func(i, j int) bool {
		return searchKeyFromItem(reference[i]).less(searchKeyFromItem(reference[j]))
	})
	if len(reference) > k {
		reference = reference[:k]
	}

	// Subject under test.
	tk := newSearchTopKHeap(k)
	for _, it := range items {
		tk.push(it)
	}
	got := tk.sorted()

	if len(got) != len(reference) {
		t.Fatalf("len(top-K) = %d, want %d", len(got), len(reference))
	}
	// Compare key-by-key. Items with the same key are interchangeable
	// (the comparator was not strictly ordering them either), so we
	// compare the *key sequence* rather than the slices of maps.
	for i := range got {
		gk := searchKeyFromItem(got[i])
		rk := searchKeyFromItem(reference[i])
		if gk != rk {
			t.Errorf("at index %d: top-K key %+v != reference key %+v", i, gk, rk)
		}
	}
}

// Edge case: fewer items than the cap. The heap should return all of them
// in order without any truncation.
func TestSearchTopKHeap_FewerThanCap(t *testing.T) {
	t.Parallel()
	tk := newSearchTopKHeap(100)
	tk.push(map[string]any{"cluster_name": "zulu", "namespace": "a", "name": "x"})
	tk.push(map[string]any{"cluster_name": "alpha", "namespace": "b", "name": "y"})
	tk.push(map[string]any{"cluster_name": "mike", "namespace": "c", "name": "z"})
	got := tk.sorted()
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0]["cluster_name"] != "alpha" || got[1]["cluster_name"] != "mike" || got[2]["cluster_name"] != "zulu" {
		t.Errorf("order = %v %v %v, want alpha/mike/zulu",
			got[0]["cluster_name"], got[1]["cluster_name"], got[2]["cluster_name"])
	}
}

// Edge case: zero-capacity cap is normalized to 1 so the heap remains
// useful. A real caller can't actually request limit=0 (queryInt clamps)
// but defensive behavior here is cheap.
func TestSearchTopKHeap_ZeroCap(t *testing.T) {
	t.Parallel()
	tk := newSearchTopKHeap(0)
	tk.push(map[string]any{"cluster_name": "a", "namespace": "x", "name": "y"})
	tk.push(map[string]any{"cluster_name": "b", "namespace": "x", "name": "y"})
	got := tk.sorted()
	if len(got) != 1 {
		t.Fatalf("len with zero cap = %d, want 1 (normalized)", len(got))
	}
	if got[0]["cluster_name"] != "a" {
		t.Errorf("kept cluster = %q, want 'a' (the smaller key)", got[0]["cluster_name"])
	}
}

func randName(rng *rand.Rand) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 6)
	for i := range b {
		b[i] = letters[rng.Intn(len(letters))]
	}
	return string(b)
}
