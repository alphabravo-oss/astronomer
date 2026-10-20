package handler

import (
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
)

func TestCatalogGlobalCandidateWindowProgress(t *testing.T) {
	for _, name := range []string{"empty-progress", "last-inspected-not-last-match", "terminal-empty", "lookahead-not-returned", "smaller-request", "oversize-refused", "duplicate-refused", "nil-identity-refused", "unchanged-position-refused"} {
		t.Run(name, func(t *testing.T) {
			page := cursorRequest{mode: true, limit: 20, binding: "binding"}
			repo := uuid.New()
			rows := make([]catalogGlobalCandidate, 17)
			for i := range rows {
				rows[i] = catalogGlobalCandidate{chart: sqlc.HelmChart{ID: uuid.New(), RepositoryID: repo, Name: "same-name"}}
			}
			if name == "last-inspected-not-last-match" {
				rows[0].matches = true
			}
			if name == "terminal-empty" {
				rows = rows[:16]
			}
			if name == "lookahead-not-returned" {
				rows[16].matches = true
			}
			if name == "smaller-request" {
				page.limit = 1
				rows = rows[:2]
				rows[0].matches = true
			}
			if name == "oversize-refused" {
				rows = append(rows, rows[0])
			}
			if name == "duplicate-refused" {
				rows[1] = rows[0]
			}
			if name == "nil-identity-refused" {
				rows[0].chart.RepositoryID = uuid.Nil
			}
			if name == "unchanged-position-refused" {
				page.provided = true
				page.cursor = paging.NewTextCursor(rows[15].chart.Name, rows[15].chart.ID, &repo, page.binding, 0)
			}
			got, next, err := catalogGlobalWindowPage(rows, page)
			refused := name == "oversize-refused" || name == "duplicate-refused" || name == "nil-identity-refused" || name == "unchanged-position-refused"
			if refused {
				if err == nil || next != nil || got != nil {
					t.Fatal("invalid candidate window accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if name == "terminal-empty" {
				if len(got) != 0 || next != nil {
					t.Fatal("empty terminal was not terminal")
				}
				return
			}
			inspected := min(int(page.limit), 16) - 1
			if next == nil || next.ID != rows[inspected].chart.ID || next.Text == nil || *next.Text != rows[inspected].chart.Name {
				t.Fatal("continuation did not anchor last inspected authorized key")
			}
			matched := 0
			if name == "last-inspected-not-last-match" || name == "smaller-request" {
				matched = 1
			}
			if len(got) != matched || next.Seen != matched {
				t.Fatal("lookahead or unmatched rows changed result count")
			}
		})
	}
}

func TestCatalogGlobalWindowMixedScopePreservesProducer(t *testing.T) {
	if !catalogGlobalWindowEligible(sqlc.CountFilteredHelmChartsParams{GlobalScope: true}) {
		t.Fatal("global scope missing")
	}
	for _, filter := range []sqlc.CountFilteredHelmChartsParams{{}, {GlobalScope: true, RepositoryIds: []uuid.UUID{uuid.New()}}, {RepositoryIds: []uuid.UUID{uuid.New()}}} {
		if catalogGlobalWindowEligible(filter) {
			t.Fatal("private repository scope entered global candidate producer")
		}
	}
}
