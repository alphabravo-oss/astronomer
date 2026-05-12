package handler

import (
	"container/heap"
	"sort"
)

// FEATURES-051126 T32 — bounded top-K heap merge for cross-cluster search.
//
// Cross-cluster search fans out to up to 16 clusters in parallel, each of
// which can return thousands of items (pods, services, etc.). Holding
// every item in a flat slice then sort+truncating to `limit` (default 100,
// cap 1000) is O(N) memory and O(N log N) time.
//
// Switching to a bounded max-heap of size `limit` lets us:
//   - keep memory at O(limit) regardless of total result volume
//   - drop the global O(N log N) sort in favor of O(N log limit) inserts
//   - preserve the fairness invariant from the prior design (every item
//     from every cluster is still *considered* — we just don't *retain*
//     items that lose to the current K smallest)
//
// The ordering matches the original sort.SliceStable: cluster_name,
// then namespace, then resource name. We implement it as a *max*-heap on
// that key so heap[0] is the *largest* item currently retained — when a
// new item arrives that is smaller, it displaces the max.

type searchKey struct {
	cluster   string
	namespace string
	name      string
}

func searchKeyFromItem(item map[string]any) searchKey {
	c, _ := item["cluster_name"].(string)
	ns, _ := item["namespace"].(string)
	n, _ := item["name"].(string)
	return searchKey{cluster: c, namespace: ns, name: n}
}

// less is the natural ascending order used by the original sort.
func (a searchKey) less(b searchKey) bool {
	if a.cluster != b.cluster {
		return a.cluster < b.cluster
	}
	if a.namespace != b.namespace {
		return a.namespace < b.namespace
	}
	return a.name < b.name
}

type searchTopKItem struct {
	key  searchKey
	item map[string]any
}

// searchTopKMaxHeap is a max-heap of searchTopKItem ordered by descending
// natural key. heap.Pop returns the *largest* element — which is what we
// evict when a smaller candidate arrives.
type searchTopKMaxHeap []searchTopKItem

func (h searchTopKMaxHeap) Len() int { return len(h) }
func (h searchTopKMaxHeap) Less(i, j int) bool {
	// Reverse natural order → max-heap.
	return h[j].key.less(h[i].key)
}
func (h searchTopKMaxHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *searchTopKMaxHeap) Push(x any) {
	*h = append(*h, x.(searchTopKItem))
}

func (h *searchTopKMaxHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

type searchTopKHeap struct {
	cap int
	h   *searchTopKMaxHeap
}

func newSearchTopKHeap(capacity int) *searchTopKHeap {
	if capacity <= 0 {
		capacity = 1
	}
	maxH := make(searchTopKMaxHeap, 0, capacity)
	return &searchTopKHeap{cap: capacity, h: &maxH}
}

// push accepts a candidate item. If the heap has room it is added
// unconditionally. Otherwise it replaces the current max only if the
// candidate's key is smaller — i.e. would survive the final sort+truncate.
func (t *searchTopKHeap) push(item map[string]any) {
	key := searchKeyFromItem(item)
	if t.h.Len() < t.cap {
		heap.Push(t.h, searchTopKItem{key: key, item: item})
		return
	}
	if key.less((*t.h)[0].key) {
		(*t.h)[0] = searchTopKItem{key: key, item: item}
		heap.Fix(t.h, 0)
	}
}

// sorted drains the heap and returns the retained items in ascending
// natural-key order — matching the prior sort.SliceStable output exactly.
func (t *searchTopKHeap) sorted() []map[string]any {
	items := make([]searchTopKItem, t.h.Len())
	copy(items, *t.h)
	sort.Slice(items, func(i, j int) bool {
		return items[i].key.less(items[j].key)
	})
	out := make([]map[string]any, len(items))
	for i, it := range items {
		out[i] = it.item
	}
	return out
}

// Len exposes the current retained count — used by tests + future
// observability hooks.
func (t *searchTopKHeap) Len() int { return t.h.Len() }
