package tasks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/google/uuid"
)

type inhibitionMatcher = jsoncontract.InhibitionMatcher

type preparedInhibition struct {
	ID uuid.UUID
	jsoncontract.InhibitionV1
}

// listEnabledInhibitions filters corrupt rows once per sweep. Its second error
// reports a lost audit intent only after valid evaluations have run; the third
// is a query failure that prevents loading a trustworthy rule set.
func listEnabledInhibitions(ctx context.Context) ([]preparedInhibition, error, error) {
	q := runtimeDependencies(ctx).Queries
	if q == nil {
		return nil, nil, nil
	}
	rows, err := q.ListEnabledAlertInhibitions(ctx)
	if err != nil {
		return nil, nil, err
	}
	valid := make([]preparedInhibition, 0, len(rows))
	auditFailures := 0
	for _, row := range rows {
		parsed, err := jsoncontract.ReadInhibitionV1(row.SourceMatchers, row.TargetMatchers, row.EqualLabels)
		if err != nil {
			category := "invalid_schema"
			var schemaErr *jsoncontract.Error
			if errors.As(err, &schemaErr) {
				category = schemaErr.Field + ":" + schemaErr.Category
			}
			runtimeLogger(ctx).WarnContext(ctx, "skipping corrupt inhibition", "rule_id", row.ID.String(), "category", category)
			if err := recordInhibitionIntegrity(ctx, q, row, category); err != nil {
				// Do not include driver errors: they can quote rejected JSON values.
				auditFailures++
			}
			continue
		}
		valid = append(valid, preparedInhibition{ID: row.ID, InhibitionV1: parsed})
	}
	if auditFailures > 0 {
		return valid, fmt.Errorf("%d inhibition integrity audit intents could not be persisted", auditFailures), nil
	}
	return valid, nil, nil
}

func recordInhibitionIntegrity(ctx context.Context, q audit.OutboxQuerier, row sqlc.AlertInhibition, category string) error {
	// Hash the complete revision, including bytes, so a repaired/re-corrupted row
	// is distinguishable even if an external writer did not update updated_at.
	// Neither the payload nor this hash enters the diagnostic or audit detail.
	revision := sha256.New()
	for _, field := range []string{row.ID.String(), category, row.UpdatedAt.UTC().Format(time.RFC3339Nano), string(row.SourceMatchers), string(row.TargetMatchers), string(row.EqualLabels)} {
		_, _ = fmt.Fprintf(revision, "%d:", len(field))
		revision.Write([]byte(field))
	}
	_, err := audit.RecordOutbox(ctx, q, audit.Event{
		Source: "worker", ActorAuthMethod: "system", Action: "alert.inhibition.integrity_failure",
		ResourceType: "alert_inhibition", ResourceID: row.ID.String(), ActionClass: "system",
		Detail: map[string]any{"category": category, "schema_version": 1},
	}, "inhibition-integrity:v1:"+hex.EncodeToString(revision.Sum(nil)), audit.OutboxOptions{})
	return err
}

// inhibitionMatchersMatch requires every matcher in a non-empty set to match.
func inhibitionMatchersMatch(ms []inhibitionMatcher, labels map[string]string) bool {
	if len(ms) == 0 {
		return false
	}
	for _, m := range ms {
		if !m.Matches(labels) {
			return false
		}
	}
	return true
}

// equalLabelsMatch requires each named label to be present on BOTH alerts and
// carry the same value. A label missing on either side is a mismatch (never
// suppress on absent labels).
func equalLabelsMatch(equal []string, source, target map[string]string) bool {
	for _, l := range equal {
		sv, sok := source[l]
		tv, tok := target[l]
		if !sok || !tok || sv != tv {
			return false
		}
	}
	return true
}

func sameLabelSet(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// alertInhibited reports whether the target alert should be suppressed: some
// enabled inhibition matches the target with its target_matchers AND a DISTINCT
// currently-firing alert matches its source_matchers AND both share equal values
// on every equal-label.
func preparedAlertInhibited(inhibitions []preparedInhibition, firing []map[string]string, targetLabels map[string]string) bool {
	for _, inh := range inhibitions {
		if !inhibitionMatchersMatch(inh.Target, targetLabels) {
			continue
		}
		for _, src := range firing {
			if sameLabelSet(src, targetLabels) {
				// An alert never inhibits itself.
				continue
			}
			if !inhibitionMatchersMatch(inh.Source, src) {
				continue
			}
			if !equalLabelsMatch(inh.Equal, src, targetLabels) {
				continue
			}
			return true
		}
	}
	return false
}
