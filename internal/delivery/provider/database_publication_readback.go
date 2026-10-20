package provider

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// PublishVerified runs after the actual publisher writes and signs child/feed
// subjects. The canonical loader authenticates immutable readback first. A
// caller's digest or command success cannot mark the reservation published.
func (owner *DatabasePublicationOwner) PublishVerified(ctx context.Context, reservation *DatabasePublicationReservation, repository, digest string) error {
	if ctx == nil || owner == nil || owner.projection == nil || reservation == nil || reservation.owner != owner || reservation.prepared == nil {
		return errors.New("database publication reservation owner differs")
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := reservation.prepared.ValidateCurrent(bounded, time.Now().UTC()); err != nil {
		return err
	}
	update, err := owner.projection.authority.LoadUpdate(bounded, owner.projection.proof, repository, digest, time.Now().UTC())
	if err != nil {
		return err
	}
	feed, document, valid := update.Snapshot()
	if !valid || feed != digest || !databasePublicationDocumentMatches(document, reservation.document) {
		return errors.New("database publication canonical signed readback differs from reserved decision")
	}
	decision, err := databaseUpdateDecisionDigest(document)
	if err != nil {
		return err
	}
	return owner.transaction(bounded, func(ctx context.Context, queries *sqlc.Queries) error {
		base, err := owner.currentBase(ctx, queries)
		if err != nil {
			return err
		}
		attempt, err := queries.GetVulnerabilityDatabaseUpdateAttemptForUpdate(ctx, reservation.attempt.ID)
		if err != nil {
			return err
		}
		if !databasePublicationAttemptMatches(attempt, base, document, decision) || !databasePublicationReservationMatches(attempt, reservation.attempt) {
			return errors.New("database publication locked reservation differs")
		}
		if err := reservation.prepared.ValidateCurrent(ctx, time.Now().UTC()); err != nil {
			return err
		}
		if err := update.ValidateCurrentMetadata(time.Now().UTC()); err != nil {
			return err
		}
		switch attempt.Phase {
		case "published":
			if !attempt.PublishedFeedDigest.Valid || attempt.PublishedFeedDigest.String != feed {
				return errors.New("database publication replay changes immutable feed")
			}
		case "reserved":
			if attempt.PublishedFeedDigest.Valid || !time.Now().UTC().Before(attempt.ExpiresAt) {
				return errors.New("database publication reservation expired or malformed")
			}
			rows, err := queries.PublishVulnerabilityDatabaseUpdateAttempt(ctx, sqlc.PublishVulnerabilityDatabaseUpdateAttemptParams{ID: attempt.ID, BaseID: base.ID, BaseEpoch: base.Epoch, Generation: document.Generation, DecisionDigest: decision, FeedDigest: databaseProjectionOptional(feed)})
			if err != nil {
				return err
			}
			if rows != 1 {
				return errors.New("database publication compare and swap lost authority")
			}
		default:
			return errors.New("database publication failed reservation cannot be restored")
		}
		if err := owner.projection.revalidateOwnedSnapshot(ctx); err != nil {
			return err
		}
		return update.ValidateCurrentMetadata(time.Now().UTC())
	})
}

// Fail is reduce-only and remains available after physical trust loss. It
// matches the owner's exact locked reservation, cannot mark a feed published,
// and cannot overwrite a previously published or failed decision.
func (owner *DatabasePublicationOwner) Fail(ctx context.Context, reservation *DatabasePublicationReservation) error {
	if ctx == nil || owner == nil || reservation == nil || reservation.owner != owner || owner.pool == nil || reservation.attempt.ID == uuid.Nil || reservation.attempt.BaseID == uuid.Nil {
		return errors.New("database publication failure owner differs")
	}
	decision, err := databaseUpdateDecisionDigest(reservation.document)
	if err != nil {
		return err
	}
	return owner.transaction(ctx, func(ctx context.Context, queries *sqlc.Queries) error {
		attempt, err := queries.GetVulnerabilityDatabaseUpdateAttemptForUpdate(ctx, reservation.attempt.ID)
		if err != nil {
			return err
		}
		if !databasePublicationReservationMatches(attempt, reservation.attempt) || attempt.Generation != reservation.document.Generation || attempt.DecisionDigest != decision {
			return errors.New("database publication failure reservation differs")
		}
		if attempt.Phase == "failed" {
			return nil
		}
		if attempt.Phase != "reserved" {
			return errors.New("database publication published reservation cannot be failed")
		}
		rows, err := queries.FailVulnerabilityDatabaseUpdateAttempt(ctx, attempt.ID)
		if err != nil {
			return err
		}
		if rows != 1 {
			return errors.New("database publication failure compare and swap changed")
		}
		return nil
	})
}
