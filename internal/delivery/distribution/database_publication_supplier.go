package distribution

import (
	"context"
	"errors"
	"path/filepath"
	"time"
)

// CapturePublicationSupplier is the connected operator producer's concrete
// supplier path. Official repository/schema selectors are fixed in code. It
// uses exactly this base's captured roots/destination transport; neither feed
// data nor a desired mutable repository can select credentials or a client.
// New supplier bytes need no invented existing Astronomer signature. They gain
// endorsement only after actual complete payload verification and publication.
func (authority *DatabaseAuthority) CapturePublicationSupplier(ctx context.Context, proof *VerifiedDatabaseAuthorityBase, role, outputPath string, now time.Time) (*CapturedSupplierDatabase, *VerifiedSupplierDatabasePayload, error) {
	if ctx == nil || authority == nil || proof == nil || proof.owner != authority || !filepath.IsAbs(outputPath) || filepath.Clean(outputPath) != outputPath || !databaseUpdateUTC(now) {
		return nil, nil, errors.New("database publication supplier authority unavailable")
	}
	repository := ""
	switch role {
	case "trivy-database":
		repository = "ghcr.io/aquasecurity/trivy-db"
	case "trivy-java-database":
		repository = "ghcr.io/aquasecurity/trivy-java-db"
	default:
		return nil, nil, errors.New("database publication supplier role unsupported")
	}
	bounded, cancel := context.WithTimeout(ctx, 16*time.Minute)
	defer cancel()
	authority.mutex.RLock()
	defer authority.mutex.RUnlock()
	if authority.closed || proof.base == nil || proof.base.owner != authority.loader || proof.fingerprint != authority.physicalFingerprint() {
		return nil, nil, errors.New("database publication supplier physical base differs")
	}
	if err := authority.revalidatePhysicalTrust(bounded); err != nil {
		return nil, nil, err
	}
	snapshot, valid := proof.base.Snapshot()
	if !valid || !snapshot.ScanningEnabled {
		return nil, nil, errors.New("database publication supplier current scanner disabled")
	}
	client := authority.loader.options.Client
	var err error
	if authority.registry != nil {
		client, err = authority.registry.PublicationSupplierClient(bounded)
		if err != nil {
			return nil, nil, err
		}
		defer client.CloseIdleConnections()
	}
	inspector, err := NewSupplierDatabaseInspector(SupplierDatabaseOptions{Repository: repository, Role: role, Scanner: snapshot.Binding.Scanner, Timing: CadenceDatabaseUpdateTiming(), Network: authority.loader.options.Network.Clone(), Client: client})
	if err != nil {
		return nil, nil, err
	}
	captured, err := inspector.Capture(bounded)
	if err != nil {
		return nil, nil, err
	}
	started := time.Now()
	_, err = inspector.Transfer(bounded, captured, outputPath, now)
	if err != nil {
		return nil, nil, err
	}
	// A long successful transfer must not renew age from completion or keep a
	// now-expired supplier snapshot. Inspect again with the elapsed clock.
	verified, err := inspector.Inspect(bounded, captured, outputPath, now.Add(time.Since(started)))
	if err != nil {
		return nil, nil, err
	}
	if err := authority.revalidatePhysicalTrust(bounded); err != nil {
		return nil, nil, err
	}
	return captured, verified, nil
}
