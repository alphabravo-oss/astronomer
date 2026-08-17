package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

const (
	defaultResolutionLease = 3 * time.Minute
	maxResolutionAttempts  = 8
	maxResolutionBatch     = 32
)

type ByteDecryptor interface {
	DecryptBytes(string) ([]byte, error)
}

type PolicyProvider interface {
	NetworkPolicy(context.Context, uuid.UUID, uuid.UUID) (NetworkPolicy, error)
	ProxyURL(context.Context, uuid.UUID, string) (string, error)
}

type PublicOnlyPolicy struct{}

func (PublicOnlyPolicy) NetworkPolicy(context.Context, uuid.UUID, uuid.UUID) (NetworkPolicy, error) {
	return NetworkPolicy{}, nil
}
func (PublicOnlyPolicy) ProxyURL(_ context.Context, _ uuid.UUID, reference string) (string, error) {
	if reference != "" {
		return "", &Error{Code: CodeNetworkDenied, Message: "source references an unknown proxy policy"}
	}
	return "", nil
}

// PostgresWorker claims resolution rows with SKIP LOCKED and a monotonically
// increasing fence. Network work happens outside transactions; final writes
// recheck the lease/fence and update the resolution, immutable bundle version,
// and source status atomically.
type PostgresWorker struct {
	pool      *pgxpool.Pool
	service   *Service
	decryptor ByteDecryptor
	policies  PolicyProvider
	owner     string
	lease     time.Duration
	now       func() time.Time
	limits    Limits
	baseCA    []byte
}

func NewPostgresWorker(pool *pgxpool.Pool, service *Service, decryptor ByteDecryptor, policies PolicyProvider, owner string) (*PostgresWorker, error) {
	if pool == nil || service == nil || decryptor == nil {
		return nil, invalid("resolution database, service, and decryptor are required")
	}
	if policies == nil {
		policies = PublicOnlyPolicy{}
	}
	if owner == "" {
		owner = "delivery-resolver-" + uuid.NewString()
	}
	if len(owner) > 128 || strings.ContainsAny(owner, "\r\n\x00") {
		return nil, invalid("resolution lease owner is invalid")
	}
	return &PostgresWorker{pool: pool, service: service, decryptor: decryptor, policies: policies, owner: owner, lease: defaultResolutionLease, now: time.Now}, nil
}

// SetLimits applies chart/operator bounds to subsequent claims. Zero fields use
// secure defaults; limits are copied so callers cannot mutate worker state.
func (w *PostgresWorker) SetLimits(limits Limits) {
	if w != nil {
		w.limits = limits.withDefaults()
	}
}

// SetBaseCABundle installs an operator-managed CA bundle used in addition to
// any source-specific CA. The caller retains no shared slice with the worker.
func (w *PostgresWorker) SetBaseCABundle(bundle []byte) {
	if w == nil {
		return
	}
	clearBytes(w.baseCA)
	w.baseCA = append([]byte(nil), bundle...)
}

func (w *PostgresWorker) Sweep(ctx context.Context, limit int) error {
	if w == nil || w.pool == nil {
		return invalid("resolution worker is not configured")
	}
	if limit <= 0 || limit > maxResolutionBatch {
		limit = maxResolutionBatch
	}
	claims, err := sqlc.New(w.pool).ClaimDeliverySourceResolutions(ctx, sqlc.ClaimDeliverySourceResolutionsParams{
		LeaseOwner: w.owner, LeaseDuration: pgtype.Interval{Microseconds: w.lease.Microseconds(), Valid: true}, ClaimLimit: int32(limit),
	})
	if err != nil {
		return fmt.Errorf("claim delivery source resolutions: %w", err)
	}
	var joined error
	for _, claim := range claims {
		if err := w.resolveClaim(ctx, claim); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func (w *PostgresWorker) ResolveOne(ctx context.Context, resolutionID uuid.UUID) error {
	if resolutionID == uuid.Nil {
		return invalid("resolution ID is required")
	}
	claim, err := sqlc.New(w.pool).ClaimDeliverySourceResolution(ctx, sqlc.ClaimDeliverySourceResolutionParams{
		LeaseOwner: w.owner, LeaseDuration: pgtype.Interval{Microseconds: w.lease.Microseconds(), Valid: true}, ID: resolutionID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim delivery source resolution: %w", err)
	}
	return w.resolveClaim(ctx, claim)
}

func (w *PostgresWorker) resolveClaim(ctx context.Context, claim sqlc.DeliverySourceResolution) error {
	queries := sqlc.New(w.pool)
	work, err := queries.GetDeliverySourceResolutionWork(ctx, sqlc.GetDeliverySourceResolutionWorkParams{
		ID: claim.ID, ExpectedLeaseOwner: w.owner, ExpectedFence: claim.Fence,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load claimed source resolution: %w", err)
	}
	request, cleanup, err := w.requestFromWork(ctx, work)
	if err != nil {
		return w.finishFailure(ctx, work, sanitize(err, CodeInvalidRequest, false))
	}
	defer cleanup()
	result, resolveErr := w.service.Resolve(ctx, request)
	if resolveErr != nil {
		return w.finishFailure(ctx, work, resolveErr)
	}
	return w.finishSuccess(ctx, work, result)
}

func (w *PostgresWorker) requestFromWork(ctx context.Context, work sqlc.GetDeliverySourceResolutionWorkRow) (Request, func(), error) {
	cleanupValues := make([][]byte, 0, 2)
	cleanup := func() {
		for _, value := range cleanupValues {
			clearBytes(value)
		}
	}
	var trust model.TrustPolicy
	if err := strictJSON(work.TrustPolicy, &trust); err != nil {
		return Request{}, cleanup, invalid("stored source trust policy is invalid")
	}
	source := model.Source{
		ID: work.SourceID, ProjectID: work.ProjectID, Name: work.SourceName,
		Description: work.SourceDescription, Type: model.SourceType(work.SourceType),
		URL: work.Url, AuthMode: model.AuthMode(work.AuthMode), Trust: trust,
	}
	var credential *CredentialMaterial
	if work.CredentialEncrypted != "" {
		plaintext, err := w.decryptor.DecryptBytes(work.CredentialEncrypted)
		if err != nil {
			return Request{}, cleanup, &Error{Code: CodeAuthentication, Message: "source credential could not be decrypted"}
		}
		cleanupValues = append(cleanupValues, plaintext)
		credential, err = decodeCredential(plaintext)
		if err != nil {
			return Request{}, cleanup, err
		}
	}
	var caBundle []byte
	if len(w.baseCA) != 0 {
		caBundle = append(caBundle, w.baseCA...)
		cleanupValues = append(cleanupValues, caBundle)
	}
	if work.CaBundleEncrypted != "" {
		plaintext, err := w.decryptor.DecryptBytes(work.CaBundleEncrypted)
		if err != nil {
			return Request{}, cleanup, &Error{Code: CodeAuthentication, Message: "source CA bundle could not be decrypted"}
		}
		cleanupValues = append(cleanupValues, plaintext)
		if len(caBundle) != 0 {
			caBundle = append(caBundle, '\n')
		}
		caBundle = append(caBundle, plaintext...)
		cleanupValues = append(cleanupValues, caBundle)
	}
	policy, err := w.policies.NetworkPolicy(ctx, work.ProjectID, work.SourceID)
	if err != nil {
		return Request{}, cleanup, &Error{Code: CodeNetworkDenied, Message: "source network policy is unavailable", Retryable: true}
	}
	proxyURL, err := w.policies.ProxyURL(ctx, work.ProjectID, work.ProxyRef)
	if err != nil {
		return Request{}, cleanup, sanitize(err, CodeNetworkDenied, false)
	}
	chart := work.ChartName
	if work.Renderer.Valid {
		var renderer model.RendererSpec
		if err := strictJSON(work.RendererSpec, &renderer); err != nil || renderer.Kind != model.RendererKind(work.Renderer.String) || renderer.Validate() != nil {
			return Request{}, cleanup, invalid("stored renderer specification is invalid")
		}
		if renderer.Helm != nil {
			if chart != "" && chart != renderer.Helm.Chart {
				return Request{}, cleanup, invalid("stored Helm chart identity is inconsistent")
			}
			chart = renderer.Helm.Chart
		}
	}
	return Request{
		Source: source, RequestedRevision: work.RequestedRevision, Chart: chart,
		Credential: credential, CABundle: caBundle, ProxyURL: proxyURL,
		NetworkPolicy: policy,
		Limits:        w.limits.withDefaults(),
	}, cleanup, nil
}

func (w *PostgresWorker) finishSuccess(ctx context.Context, work sqlc.GetDeliverySourceResolutionWorkRow, result Result) error {
	return w.transaction(ctx, func(queries *sqlc.Queries) error {
		if _, err := queries.CompleteDeliverySourceResolution(ctx, sqlc.CompleteDeliverySourceResolutionParams{
			ResolvedRevision: result.Revision.Value, ArtifactDigest: result.Revision.ArtifactDigest.String(),
			VerificationStatus: result.Verification.Status, VerificationIdentity: result.Verification.Identity,
			ID: work.ID, ExpectedLeaseOwner: w.owner, ExpectedFence: work.Fence,
		}); err != nil {
			return err
		}
		if work.BundleVersionID.Valid {
			draft, dependencies, err := draftFromWork(work)
			if err != nil {
				return err
			}
			resolvedSpec, err := draft.Resolve(result.Revision)
			if err != nil {
				return err
			}
			digest, err := model.CanonicalDigest(struct {
				Spec         model.BundleVersionSpec `json:"spec"`
				Dependencies []uuid.UUID             `json:"dependency_bundle_ids"`
			}{resolvedSpec, dependencies})
			if err != nil {
				return err
			}
			resolvedSource := model.ResolvedSourceSpec{
				SourceID: work.SourceID, Type: model.SourceType(work.SourceType), URL: work.Url,
				AuthMode: model.AuthMode(work.AuthMode), Trust: draftTrust(work.TrustPolicy), Revision: result.Revision,
			}
			sourceJSON, err := json.Marshal(resolvedSource)
			if err != nil {
				return err
			}
			if _, err := queries.ResolveComponentBundleVersion(ctx, sqlc.ResolveComponentBundleVersionParams{
				ResolvedRevision: result.Revision.Value, ArtifactDigest: result.Revision.ArtifactDigest.String(),
				SourceSpec: sourceJSON, SpecDigest: digest.String(), VerificationStatus: result.Verification.Status,
				VerificationIdentity: result.Verification.Identity, ID: work.BundleVersionID.Bytes,
			}); err != nil {
				return err
			}
		}
		now := w.now().UTC()
		_, err := queries.UpdateDeliverySourceStatus(ctx, sqlc.UpdateDeliverySourceStatusParams{
			Status: "ready", LastResolvedAt: pgtype.Timestamptz{Time: now, Valid: true}, LastErrorCode: "", UpdatedBy: pgtype.UUID{}, ID: work.SourceID,
		})
		return err
	})
}

func (w *PostgresWorker) finishFailure(ctx context.Context, work sqlc.GetDeliverySourceResolutionWorkRow, resolutionErr error) error {
	var typed *Error
	if !errors.As(resolutionErr, &typed) {
		typed = &Error{Code: CodeUpstreamTemporary, Message: "source operation failed", Retryable: true}
	}
	permanent := !typed.Retryable || work.ResolutionAttempt >= maxResolutionAttempts
	return w.transaction(ctx, func(queries *sqlc.Queries) error {
		if permanent {
			if _, err := queries.FailDeliverySourceResolution(ctx, sqlc.FailDeliverySourceResolutionParams{
				VerificationStatus: "failed", ErrorCode: string(typed.Code), ID: work.ID,
				ExpectedLeaseOwner: w.owner, ExpectedFence: work.Fence,
			}); err != nil {
				return err
			}
			if work.BundleVersionID.Valid {
				if _, err := queries.FailComponentBundleVersion(ctx, sqlc.FailComponentBundleVersionParams{
					VerificationStatus: "failed", LastErrorCode: string(typed.Code), ID: work.BundleVersionID.Bytes,
				}); err != nil {
					return err
				}
			}
		} else {
			next := w.now().UTC().Add(resolutionBackoff(work.ResolutionAttempt))
			if _, err := queries.RetryDeliverySourceResolution(ctx, sqlc.RetryDeliverySourceResolutionParams{
				ErrorCode: string(typed.Code), NextAttemptAt: next, ID: work.ID,
				ExpectedLeaseOwner: w.owner, ExpectedFence: work.Fence,
			}); err != nil {
				return err
			}
		}
		_, err := queries.UpdateDeliverySourceStatus(ctx, sqlc.UpdateDeliverySourceStatusParams{
			Status: "degraded", LastResolvedAt: pgtype.Timestamptz{}, LastErrorCode: string(typed.Code), UpdatedBy: pgtype.UUID{}, ID: work.SourceID,
		})
		return err
	})
}

func (w *PostgresWorker) transaction(ctx context.Context, fn func(*sqlc.Queries) error) error {
	tx, err := w.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(sqlc.New(tx)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	return tx.Commit(ctx)
}

func decodeCredential(raw []byte) (*CredentialMaterial, error) {
	var values map[string]string
	if err := strictJSON(raw, &values); err != nil || len(values) > 6 {
		return nil, &Error{Code: CodeAuthentication, Message: "stored source credential is invalid"}
	}
	allowed := map[string]bool{"username": true, "password": true, "bearerToken": true, "identity": true, "known_hosts": true}
	for key := range values {
		if !allowed[key] {
			return nil, &Error{Code: CodeAuthentication, Message: "stored source credential is invalid"}
		}
	}
	return &CredentialMaterial{
		Username: []byte(values["username"]), Password: []byte(values["password"]), Token: []byte(values["bearerToken"]),
		PrivateKey: []byte(values["identity"]), KnownHosts: []byte(values["known_hosts"]), Passphrase: []byte(values["password"]),
	}, nil
}

func draftFromWork(work sqlc.GetDeliverySourceResolutionWorkRow) (model.BundleVersionDraft, []uuid.UUID, error) {
	if !work.Renderer.Valid || !work.Scope.Valid {
		return model.BundleVersionDraft{}, nil, invalid("bundle resolution is missing renderer metadata")
	}
	var renderer model.RendererSpec
	var reconciliation model.ReconciliationPolicy
	var requirements []model.CapabilityRequirement
	var dependencies []uuid.UUID
	if strictJSON(work.RendererSpec, &renderer) != nil || strictJSON(work.ReconciliationPolicy, &reconciliation) != nil ||
		strictJSON(work.Requirements, &requirements) != nil || strictJSON(work.DependencyBundleIds, &dependencies) != nil {
		return model.BundleVersionDraft{}, nil, invalid("stored bundle draft is invalid")
	}
	draft := model.BundleVersionDraft{
		SourceID: work.SourceID, RequestedRevision: work.RequestedRevision, Renderer: renderer,
		Scope: model.Scope(work.Scope.String), Reconciliation: reconciliation, RequiredCapabilities: requirements,
	}
	if err := draft.Validate(); err != nil {
		return model.BundleVersionDraft{}, nil, invalid("stored bundle draft is invalid")
	}
	return draft, dependencies, nil
}

func draftTrust(raw []byte) model.TrustPolicy {
	var trust model.TrustPolicy
	_ = strictJSON(raw, &trust)
	return trust
}

func strictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func resolutionBackoff(attempt int32) time.Duration {
	exponent := math.Min(float64(max(0, attempt-1)), 7)
	return time.Duration(math.Pow(2, exponent)) * 15 * time.Second
}
