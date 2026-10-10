package distribution

import (
	"context"
	"errors"
	"time"
)

// ObservedDatabaseTrustLoss is a short-lived observation by this exact physical
// constructor. It authorizes no acceptance, promotion or scan. Caller DATA and
// a closed authority cannot create it; the previous opaque base must exist.
type ObservedDatabaseTrustLoss struct {
	owner   *DatabaseAuthority
	base    *VerifiedDatabaseAuthorityBase
	expires time.Time
}

// ObserveTrustLoss observes canonical mounted-material revalidation only; it
// performs no registry requests or signature-policy changes. A live constructor
// may revoke its exact historical durable base after a mount disappears or
// changes. A fresh daemon unable to capture that base needs separate recovery
// authority; this operation deliberately does not claim to establish it.
func (authority *DatabaseAuthority) ObserveTrustLoss(ctx context.Context, proof *VerifiedDatabaseAuthorityBase) (*ObservedDatabaseTrustLoss, error) {
	if ctx == nil || authority == nil || proof == nil || proof.owner != authority {
		return nil, errors.New("database trust loss physical owner unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	authority.mutex.RLock()
	defer authority.mutex.RUnlock()
	if authority.closed || proof.base == nil || proof.base.owner != authority.loader || proof.fingerprint != authority.physicalFingerprint() {
		return nil, errors.New("database trust loss physical owner unavailable")
	}
	if err := authority.observedPhysicalTrustLoss(ctx); err != nil {
		return nil, err
	}
	return &ObservedDatabaseTrustLoss{owner: authority, base: proof, expires: time.Now().UTC().Add(30 * time.Second)}, nil
}

// RevalidateTrustLoss reevaluates the same loss before the owning revocation
// commit. Recovery of the original material, close, expiry or cancellation
// invalidates the observation instead of silently converting it into authority.
func (authority *DatabaseAuthority) RevalidateTrustLoss(ctx context.Context, loss *ObservedDatabaseTrustLoss) error {
	if ctx == nil || authority == nil || loss == nil || loss.owner != authority || loss.base == nil || loss.base.owner != authority {
		return errors.New("database trust loss observation unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	authority.mutex.RLock()
	defer authority.mutex.RUnlock()
	if authority.closed || loss.base.base == nil || loss.base.base.owner != authority.loader || loss.base.fingerprint != authority.physicalFingerprint() || !time.Now().UTC().Before(loss.expires) {
		return errors.New("database trust loss observation unavailable")
	}
	return authority.observedPhysicalTrustLoss(ctx)
}

// Called under the actual constructor lock. Failure of canonical material
// validation is a loss observation only while the owning context remains live.
// Context errors never authorize a durable write. Raw path/PEM errors stay out
// of the returned observation and diagnostic.
func (authority *DatabaseAuthority) observedPhysicalTrustLoss(ctx context.Context) error {
	err := authority.revalidatePhysicalTrust(ctx)
	if contextError := ctx.Err(); contextError != nil {
		return contextError
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if err == nil {
		return errors.New("database physical trust remains current")
	}
	return nil
}
