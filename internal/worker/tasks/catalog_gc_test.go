package tasks

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// fakeVersionRow models a helm chart version row for GC unit tests.
type fakeVersionRow struct {
	ID      uuid.UUID
	Version string
}

// TestGCPagedOrphansDeletesAllBeyondFirstPage is the CORR-R04 regression for
// catalog GC: when more than pageSize rows all need deletion, a naive
// offset+=pageSize loop leaves the second page empty after the first page
// deletes (rows compact) and orphans remain. gcPagedOrphans must delete all.
func TestGCPagedOrphansDeletesAllBeyondFirstPage(t *testing.T) {
	const pageSize int32 = 1000
	const total = 1500 // > pageSize; all are orphans (seen is empty)

	store := make([]fakeVersionRow, 0, total)
	for i := 0; i < total; i++ {
		store = append(store, fakeVersionRow{
			ID:      uuid.New(),
			Version: fmt.Sprintf("1.0.%d", i),
		})
	}
	seen := map[string]struct{}{} // nothing kept — every version is an orphan
	deletedIDs := map[uuid.UUID]struct{}{}

	listPage := func(limit, offset int32) []fakeVersionRow {
		if offset < 0 {
			offset = 0
		}
		if int(offset) >= len(store) {
			return nil
		}
		end := int(offset) + int(limit)
		if end > len(store) {
			end = len(store)
		}
		out := make([]fakeVersionRow, end-int(offset))
		copy(out, store[offset:end])
		return out
	}

	deleteByID := func(id uuid.UUID) {
		for i, row := range store {
			if row.ID == id {
				store = append(store[:i], store[i+1:]...)
				deletedIDs[id] = struct{}{}
				return
			}
		}
	}

	err := gcPagedOrphans(pageSize, func(limit, offset int32) (int, int, error) {
		page := listPage(limit, offset)
		deleted := 0
		for _, row := range page {
			if _, ok := seen[row.Version]; ok {
				continue
			}
			deleteByID(row.ID)
			deleted++
		}
		return len(page), deleted, nil
	})
	if err != nil {
		t.Fatalf("gcPagedOrphans: %v", err)
	}
	if len(store) != 0 {
		t.Fatalf("orphans remaining = %d, want 0 (silent incomplete GC)", len(store))
	}
	if len(deletedIDs) != total {
		t.Fatalf("deleted = %d, want %d", len(deletedIDs), total)
	}
}

// TestGCPagedOrphansKeepsSeenAndDeletesOrphansAcrossPages mixes keepers and
// orphans beyond one page so offset advances only over full keeper pages.
func TestGCPagedOrphansKeepsSeenAndDeletesOrphansAcrossPages(t *testing.T) {
	const pageSize int32 = 100
	// 250 keepers + 250 orphans interleaved by suffix.
	store := make([]fakeVersionRow, 0, 500)
	seen := map[string]struct{}{}
	for i := 0; i < 500; i++ {
		v := fmt.Sprintf("v-%04d", i)
		id := uuid.New()
		store = append(store, fakeVersionRow{ID: id, Version: v})
		if i%2 == 0 {
			seen[v] = struct{}{} // keep even
		}
	}
	wantKeep := 250

	listPage := func(limit, offset int32) []fakeVersionRow {
		if int(offset) >= len(store) {
			return nil
		}
		end := int(offset) + int(limit)
		if end > len(store) {
			end = len(store)
		}
		out := make([]fakeVersionRow, end-int(offset))
		copy(out, store[offset:end])
		return out
	}
	deleteByID := func(id uuid.UUID) {
		for i, row := range store {
			if row.ID == id {
				store = append(store[:i], store[i+1:]...)
				return
			}
		}
	}

	if err := gcPagedOrphans(pageSize, func(limit, offset int32) (int, int, error) {
		page := listPage(limit, offset)
		deleted := 0
		for _, row := range page {
			if _, ok := seen[row.Version]; ok {
				continue
			}
			deleteByID(row.ID)
			deleted++
		}
		return len(page), deleted, nil
	}); err != nil {
		t.Fatalf("gcPagedOrphans: %v", err)
	}
	if len(store) != wantKeep {
		t.Fatalf("remaining rows = %d, want %d keepers", len(store), wantKeep)
	}
	for _, row := range store {
		if _, ok := seen[row.Version]; !ok {
			t.Fatalf("orphan %q still present after GC", row.Version)
		}
	}
}

// TestGCPagedOrphansNaiveOffsetWouldLeaveOrphans documents the bug class:
// advancing offset after deletes on a full orphan page leaves ~N-pageSize rows.
func TestGCPagedOrphansNaiveOffsetWouldLeaveOrphans(t *testing.T) {
	const pageSize = 1000
	const total = 1500
	store := make([]int, total)
	for i := range store {
		store[i] = i
	}
	// Naive loop (the pre-fix bug).
	for offset := 0; ; offset += pageSize {
		if offset >= len(store) {
			break
		}
		end := offset + pageSize
		if end > len(store) {
			end = len(store)
		}
		page := append([]int(nil), store[offset:end]...)
		// delete all in page (all orphans)
		for _, id := range page {
			for j, v := range store {
				if v == id {
					store = append(store[:j], store[j+1:]...)
					break
				}
			}
		}
		if len(page) < pageSize {
			break
		}
	}
	// After first page deleted 1000, store has 500 left but offset becomes 1000
	// and the loop exits with orphans remaining.
	if len(store) == 0 {
		t.Fatal("expected naive algorithm to leave orphans (fixture of the bug); got empty store")
	}
	if len(store) != total-pageSize {
		t.Fatalf("naive leftover = %d, want %d", len(store), total-pageSize)
	}
}
