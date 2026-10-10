package userpreferences

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrNotConfigured = errors.New("preference service not configured")
var ErrInvalidStored = errors.New("stored preference document is invalid")
var ErrConflict = errors.New("idempotency key identifies a different preference document")

type Reader interface {
	GetUserPreferences(context.Context, uuid.UUID) (sqlc.UserPreference, error)
}
type MutationTx interface {
	audit.OutboxQuerier
	UpsertUserPreferences(context.Context, sqlc.UpsertUserPreferencesParams) (sqlc.UserPreference, error)
}
type ReceiptQueries interface {
	ReserveOperationIdempotencyKey(context.Context, sqlc.ReserveOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error)
	AttachOperationIdempotencyKey(context.Context, sqlc.AttachOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error)
}
type RunTx func(context.Context, func(MutationTx) error) error

// Replay is explicit, transport-neutral operation identity. HTTP composition
// validates the header and resolves the current actor and route before passing
// it. Empty Scope/Key means an ordinary full replacement, never a replay.
type Replay struct{ Scope, Key, Method, Path string }

// Command is created only by Prepare. It retains normalized, validated values
// and their deterministic digest; callers cannot substitute an unvalidated DTO.
type Command struct {
	document Preferences
	digest   string
}

func (c Command) Document() Preferences { return cloneDocument(c.document) }
func (c Command) Digest() string        { return c.digest }

func cloneDocument(p Preferences) Preferences {
	p.Favorites = slices.Clone(p.Favorites)
	p.PinnedClusters = slices.Clone(p.PinnedClusters)
	p.StarredTypes = slices.Clone(p.StarredTypes)
	return p
}

func DocumentDigest(p Preferences) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", sum), nil
}
func Prepare(p Preferences) (Command, error) {
	p = cloneDocument(p.NormalizeOptional())
	if err := p.Validate(); err != nil {
		return Command{}, err
	}
	digest, err := DocumentDigest(p)
	if err != nil {
		return Command{}, err
	}
	return Command{document: p, digest: digest}, nil
}

type Service struct {
	reader Reader
	runTx  RunTx
}

// New receives the canonical read adapter and mandatory transaction owner.
// Nil capabilities remain explicitly unavailable, supporting existing read-only
// composition without a fallback writer or global service locator.
func New(reader Reader, runTx RunTx) *Service { return &Service{reader: reader, runTx: runTx} }
func (s *Service) CanRead() bool              { return s != nil && s.reader != nil }
func (s *Service) CanReplace() bool           { return s != nil && s.runTx != nil }
func (s *Service) Get(ctx context.Context, actor uuid.UUID) (Preferences, error) {
	if !s.CanRead() {
		return Preferences{}, ErrNotConfigured
	}
	row, err := s.reader.GetUserPreferences(ctx, actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return Defaults(), nil
	}
	if err != nil {
		return Preferences{}, err
	}
	p, err := FromRow(row)
	if err != nil {
		return Preferences{}, fmt.Errorf("%w: %w", ErrInvalidStored, err)
	}
	return p, nil
}

func (s *Service) Replace(ctx context.Context, actor uuid.UUID, command Command, replay Replay, intent audit.Intent) (Preferences, error) {
	var response Preferences
	if !s.CanReplace() {
		return response, ErrNotConfigured
	}
	if actor == uuid.Nil || command.digest == "" {
		return response, ErrReceiptInvalid
	}
	// Recheck the immutable command against its document before starting work;
	// this also protects slice values changed by the caller after Prepare.
	digest, err := DocumentDigest(command.document)
	if err != nil || digest != command.digest || command.document.Validate() != nil {
		return response, ErrReceiptInvalid
	}
	params, err := replacementParams(actor, command.document)
	if err != nil {
		return response, err
	}
	keyed := replay.Scope != "" || replay.Key != ""
	if keyed && (replay.Scope == "" || replay.Key == "" || replay.Method == "" || replay.Path == "") {
		return response, ErrReceiptInvalid
	}
	err = s.runTx(ctx, func(q MutationTx) error {
		var receipts ReceiptQueries
		if keyed {
			var ok bool
			receipts, ok = q.(ReceiptQueries)
			if !ok {
				return ErrReceiptUnavailable
			}
			prior, isReplay, err := claimPreferenceReceipt(ctx, receipts, replay, actor, digest)
			if err != nil {
				return err
			}
			if isReplay {
				response = prior
				return nil
			}
		}
		stored, err := q.UpsertUserPreferences(ctx, params)
		if err != nil {
			return err
		}
		response, err = FromRow(stored)
		if err != nil {
			return err
		}
		storedDigest, err := DocumentDigest(response)
		if err != nil || storedDigest != digest {
			return ErrReceiptInvalid
		}
		if err = audit.RecordIntent(ctx, q, intent); err != nil {
			return err
		}
		if keyed {
			return attachPreferenceReceipt(ctx, receipts, replay, actor, digest, response)
		}
		return nil
	})
	return response, err
}

func replacementParams(actor uuid.UUID, p Preferences) (sqlc.UpsertUserPreferencesParams, error) {
	var zero sqlc.UpsertUserPreferencesParams
	favorites, err := json.Marshal(p.Favorites)
	if err != nil {
		return zero, err
	}
	pinned, err := json.Marshal(p.PinnedClusters)
	if err != nil {
		return zero, err
	}
	starred, err := json.Marshal(p.StarredTypes)
	if err != nil {
		return zero, err
	}
	return sqlc.UpsertUserPreferencesParams{UserID: actor, Theme: string(p.Theme), TableDensity: string(p.TableDensity), LandingRoute: p.LandingRoute, TimeFormat: string(p.TimeFormat), Favorites: favorites, PinnedClusters: pinned, StarredTypes: starred, RowsPerPage: int32(p.RowsPerPage), DateFormat: p.DateFormat}, nil
}
