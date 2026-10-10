package distribution

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
)

const MaximumOfflineDatabaseUpdateMetadataBytes int64 = 8 << 20

// OfflineDatabaseUpdateInput belongs to the protected operator import command.
// Its addresses identify original signed artifacts; no field grants a network,
// signing, trust, mutation, assignment or publication permission.
type OfflineDatabaseUpdateInput struct {
	Repository, Digest               string
	Subjects                         []StoredSubject
	Blobs                            map[string][]byte
	MainPayloadPath, JavaPayloadPath string
}

// VerifiedOfflineDatabaseUpdate owns a canonical signed feed/child graph and
// complete inspected local payloads. It does not grant local publication or
// acceptance. The private import owner must reserve an accurately labeled
// imported decision and use the existing locked canonical acceptance path.
type VerifiedOfflineDatabaseUpdate struct {
	mutex      sync.Mutex
	closed     bool
	files      [2]*os.File
	identities [2]os.FileInfo
	owner      *DatabaseAuthority
	proof      *VerifiedDatabaseAuthorityBase
	repository string
	update     *AuthenticatedDatabaseUpdate
	store      *offlineRegistry
	paths      [2]string
	main, java *VerifiedDatabaseUpdatePayload
}

// CaptureOfflineUpdate authenticates every original signature before opening
// either large payload. No DNS or network transport exists in the closed store.
// The existing mounted vendor/mirror/TLS authority is independently revalidated;
// disconnected bytes can neither choose it nor broaden destination permission.
func (authority *DatabaseAuthority) CaptureOfflineUpdate(ctx context.Context, proof *VerifiedDatabaseAuthorityBase, input OfflineDatabaseUpdateInput, now time.Time) (*VerifiedOfflineDatabaseUpdate, error) {
	if ctx == nil || !databaseUpdateUTC(now) || !filepath.IsAbs(input.MainPayloadPath) || !filepath.IsAbs(input.JavaPayloadPath) || filepath.Clean(input.MainPayloadPath) != input.MainPayloadPath || filepath.Clean(input.JavaPayloadPath) != input.JavaPayloadPath || input.MainPayloadPath == input.JavaPayloadPath {
		return nil, errors.New("offline database update owned input invalid")
	}
	if err := authority.Revalidate(ctx, proof); err != nil {
		return nil, err
	}
	started := time.Now()
	store, err := newOfflineDatabaseUpdateRegistry(input.Subjects, input.Blobs)
	if err != nil {
		return nil, err
	}
	captured := &VerifiedOfflineDatabaseUpdate{owner: authority, proof: proof, repository: input.Repository, store: store, paths: [2]string{input.MainPayloadPath, input.JavaPayloadPath}}
	update, err := captured.loadMetadata(ctx, input.Digest, now)
	if err != nil {
		return nil, err
	}
	captured.update = update
	retained := false
	defer func() {
		if !retained {
			captured.Close()
		}
	}()
	// Both roles are complete and inspected using the same bounded two-pass
	// compressed-byte authentication and selected archive/metadata parser.
	for index, role := range []string{"trivy-database", "trivy-java-database"} {
		payload, err := InspectDatabaseUpdatePayload(ctx, update, role, captured.paths[index], CadenceDatabaseUpdateTiming(), now)
		if err != nil {
			return nil, err
		}
		_, snapshot, valid := payload.Snapshot()
		if !valid {
			return nil, errors.New("offline database update inspected snapshot unavailable")
		}
		captured.files[index], captured.identities[index], err = openDatabaseInspectionFile(captured.paths[index], snapshot.Layer.Size)
		if err != nil {
			return nil, err
		}
		if err := hashDatabaseInspectionFile(ctx, captured.files[index], snapshot.Layer); err != nil {
			return nil, err
		}
		if !sameDatabaseInspectionFile(captured.files[index], captured.paths[index], captured.identities[index]) {
			return nil, errors.New("offline database update pin changed after complete hash")
		}
		if index == 0 {
			captured.main = payload
		} else {
			captured.java = payload
		}
	}
	if err := captured.ValidateCurrent(ctx, authority, proof, now.Add(time.Since(started))); err != nil {
		return nil, err
	}
	retained = true
	return captured, nil
}

func newOfflineDatabaseUpdateRegistry(subjects []StoredSubject, blobs map[string][]byte) (*offlineRegistry, error) {
	if len(subjects) != 3 || len(blobs) == 0 || len(blobs) > 24 {
		return nil, errors.New("offline database update graph count differs")
	}
	var total int64
	for _, body := range blobs {
		if len(body) == 0 || len(body) > 1<<20 || int64(len(body)) > MaximumOfflineDatabaseUpdateMetadataBytes-total {
			return nil, errors.New("offline database update metadata exceeds finite bounds")
		}
		total += int64(len(body))
	}
	return newOfflineRegistryGraph(subjects, blobs, true)
}

func (captured *VerifiedOfflineDatabaseUpdate) loadMetadata(ctx context.Context, feed string, now time.Time) (*AuthenticatedDatabaseUpdate, error) {
	if captured == nil || captured.owner == nil || captured.store == nil {
		return nil, errors.New("offline database update owner unavailable")
	}
	if err := captured.owner.Revalidate(ctx, captured.proof); err != nil {
		return nil, err
	}
	snapshot, valid := captured.proof.Snapshot()
	if !valid {
		return nil, errors.New("offline database update current base unavailable")
	}
	captured.owner.mutex.RLock()
	options := captured.owner.loader.options
	trustDirectory := captured.owner.trust.FrozenDirectory()
	captured.owner.mutex.RUnlock()
	// A fresh closed transport copy keeps per-verification usage counters private.
	var subjects []StoredSubject
	for identity, subject := range captured.store.manifests {
		repository, reference, found := strings.Cut(identity, "@")
		if !found || !digestPattern.MatchString(reference) {
			continue
		}
		signatureTag := "sha256-" + strings.TrimPrefix(reference, "sha256:") + ".sig"
		subjects = append(subjects, StoredSubject{Repository: repository, Digest: subject, SignatureDigest: captured.store.manifests[repository+"@"+signatureTag]})
	}
	store, err := newOfflineDatabaseUpdateRegistry(subjects, captured.store.blobs)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: store, Timeout: time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	loader, err := NewDatabaseUpdateLoader(DatabaseUpdateOptions{Repository: captured.repository, Digest: feed, Binding: snapshot.Base.Binding, Timing: CadenceDatabaseUpdateTiming(), JavaRequired: true, Policy: options.Policy, TrustDirectory: trustDirectory, Network: options.Network, Client: client})
	if err != nil {
		return nil, err
	}
	loader.offline = store
	update, err := loader.Load(ctx, now)
	if err != nil {
		return nil, err
	}
	_, document, valid := update.Snapshot()
	if !valid || len(document.Databases) != 2 || len(store.databases) != 2 {
		return nil, errors.New("offline database update exact two-role graph required")
	}
	expected := map[string]bool{captured.repository + "@" + feed: true}
	for _, database := range document.Databases {
		expected[database.PublishedSubject] = true
		missing, exists := store.databases[database.Layer.Digest]
		if !exists || missing.MediaType != database.Layer.MediaType || missing.Size != database.Layer.Size {
			return nil, errors.New("offline database update signed missing layer differs")
		}
	}
	if len(expected) != 3 || len(store.used) != 3 {
		return nil, errors.New("offline database update contains extra or overlapping subject")
	}
	for subject := range expected {
		if !store.used[subject] {
			return nil, errors.New("offline database update graph differs from signed exact subjects")
		}
	}
	if err := captured.owner.Revalidate(ctx, captured.proof); err != nil {
		return nil, err
	}
	return update, nil
}

func (captured *VerifiedOfflineDatabaseUpdate) ValidateCurrent(ctx context.Context, authority *DatabaseAuthority, proof *VerifiedDatabaseAuthorityBase, now time.Time) error {
	if ctx == nil || captured == nil || authority != captured.owner || proof != captured.proof || captured.update == nil || !databaseUpdateUTC(now) {
		return errors.New("offline database update current owner differs")
	}
	captured.mutex.Lock()
	defer captured.mutex.Unlock()
	if captured.closed {
		return errors.New("offline database update capture closed")
	}
	feed, document, valid := captured.update.Snapshot()
	if !valid {
		return errors.New("offline database update authenticated decision unavailable")
	}
	current, err := captured.loadMetadata(ctx, feed, now)
	if err != nil {
		return err
	}
	_, observed, valid := current.Snapshot()
	if !valid || !reflect.DeepEqual(observed, document) {
		return errors.New("offline database update immutable decision changed")
	}
	for index, previous := range []*VerifiedDatabaseUpdatePayload{captured.main, captured.java} {
		originalFeed, _, valid := previous.Snapshot()
		if !valid || originalFeed != feed {
			return errors.New("offline database update inspected payload unavailable")
		}
		if captured.files[index] == nil || captured.identities[index] == nil || !sameDatabaseInspectionFile(captured.files[index], captured.paths[index], captured.identities[index]) {
			return errors.New("offline database update pinned original changed")
		}
		// Pinned identity survives every acceptance commit. The streaming importer
		// must separately rehash before and during copy; it cannot send bytes from
		// this historical proof alone. Privileged same-inode/mtime forgery remains
		// the explicit host boundary, identical to the canonical refresh capture.

	}
	return authority.Revalidate(ctx, proof)
}

// Snapshot is comparison DATA. Local imported disposition, current base and
// per-cluster assignment acceptance remain private transaction owner decisions.
func (captured *VerifiedOfflineDatabaseUpdate) Snapshot() (string, DatabaseUpdateDocument, DatabaseUpdateSnapshot, DatabaseUpdateSnapshot, bool) {
	if captured == nil || captured.owner == nil || captured.update == nil {
		return "", DatabaseUpdateDocument{}, DatabaseUpdateSnapshot{}, DatabaseUpdateSnapshot{}, false
	}
	captured.mutex.Lock()
	defer captured.mutex.Unlock()
	if captured.closed {
		return "", DatabaseUpdateDocument{}, DatabaseUpdateSnapshot{}, DatabaseUpdateSnapshot{}, false
	}
	feed, document, valid := captured.update.Snapshot()
	mainFeed, main, mainValid := captured.main.Snapshot()
	javaFeed, java, javaValid := captured.java.Snapshot()
	return feed, document, main, java, valid && mainValid && javaValid && mainFeed == feed && javaFeed == feed
}

// Close only releases owned pinned input FDs after synchronous import/acceptance
// joins. It never removes operator kit files or claims active request drain.
func (captured *VerifiedOfflineDatabaseUpdate) Close() {
	if captured == nil {
		return
	}
	captured.mutex.Lock()
	defer captured.mutex.Unlock()
	if captured.closed {
		return
	}
	captured.closed = true
	for index, file := range captured.files {
		if file != nil {
			_ = file.Close()
			captured.files[index] = nil
		}
	}
}

// OriginalSubject is comparison DATA from the authenticated closed graph.
func (captured *VerifiedOfflineDatabaseUpdate) OriginalSubject() (string, bool) {
	if captured == nil {
		return "", false
	}
	captured.mutex.Lock()
	defer captured.mutex.Unlock()
	if captured.closed || captured.update == nil {
		return "", false
	}
	feed, _, valid := captured.update.Snapshot()
	return captured.repository + "@" + feed, valid
}

// CheckReadbackCapacity includes the original complete kit, conservative index
// and metadata bounds, the two additional local readback layers and reserve.
// It never raises caps to admit two maximally sized layers simultaneously.
func (captured *VerifiedOfflineDatabaseUpdate) CheckReadbackCapacity(ctx context.Context, authority *DatabaseAuthority, proof *VerifiedDatabaseAuthorityBase, directory string) error {
	if err := captured.ValidateCurrent(ctx, authority, proof, time.Now().UTC()); err != nil {
		return err
	}
	_, _, main, java, valid := captured.Snapshot()
	if !valid {
		return errors.New("offline database readback layer closure unavailable")
	}
	layers := main.Layer.Size + java.Layer.Size
	if layers <= 0 || layers > MaximumOfflineDatabaseUpdateKitBytes/2 || 2*layers+MaximumOfflineDatabaseUpdateMetadataBytes+(64<<10)+SupplierDatabaseTransferReserveBytes > MaximumOfflineDatabaseUpdateKitBytes {
		return errors.New("offline database original plus readback aggregate exceeds finite kit budget")
	}
	root, _, err := openSupplierTransferDirectory(directory, min(layers, MaximumDatabaseLayerBytes))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return supplierRefreshCapacity(root, layers)
}
