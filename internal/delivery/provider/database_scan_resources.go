package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"time"

	"github.com/google/uuid"
	digest "github.com/opencontainers/go-digest"
	corev1 "k8s.io/api/core/v1"
	networkv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
)

// An API write is bound to one exact immutable canonical object. An early
// conflict can only reuse a readback with current identity/spec/content checks;
// no existing object is patched, adopted by name, or deleted by this owner.
func (owner *DatabaseScanOwner) ensureOwnedObject(ctx context.Context, claim sqlc.VulnerabilityDatabaseScanClaim, collection, path string, object any, readOnly bool) ([]byte, error) {
	if err := owner.revalidateScanMaterial(ctx); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(object)
	if err != nil || len(payload) == 0 || len(payload) > databaseScanMaximumAPIBytes {
		clear(payload)
		return nil, errors.New("owned database object request exceeds bound")
	}
	defer clear(payload)
	scoped, err := newDatabaseScanScopedRequester(owner.requester, claim.ClusterID, []string{path}, map[string]string{collection: digest.FromBytes(payload).String()})
	if err != nil {
		return nil, err
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if !readOnly {
		if err := owner.registerScanResourceIntent(bounded, claim, path, digest.FromBytes(payload).String()); err != nil {
			return nil, err
		}
		// Revalidate the actual locked current claim/receipt before an API mutation.
		// Kubernetes and PostgreSQL cannot share an atomic commit; completion still
		// rechecks every current resource and target before any report witness.
		if err := owner.transitionClaim(bounded, claim, func(context.Context, *sqlc.Queries, sqlc.VulnerabilityDatabaseScanClaim) error { return nil }); err != nil {
			return nil, err
		}
		response, err := scoped.Do(bounded, claim.ClusterID.String(), http.MethodPost, collection, payload, map[string]string{"Content-Type": "application/json"})
		if bounded.Err() != nil {
			return nil, bounded.Err()
		}
		if err != nil || response == nil || (response.StatusCode != 201 && response.StatusCode != 409) {
			return nil, errors.New("owned database object creation unavailable")
		}
	}
	// The creation response is never permission or a physical trust proof. Actual
	// current GET is mandatory, including on success and on idempotent conflict.
	// A completion recheck passes literal readOnly=true: no POST can occur.
	current, err := databaseScanGetBytes(bounded, scoped, claim.ClusterID, path, databaseScanMaximumAPIBytes)
	if err != nil {
		return nil, err
	}
	if err := owner.revalidateScanMaterial(bounded); err != nil {
		clear(current)
		return nil, err
	}
	return current, nil
}
func ownedDatabaseObjectMetadata(meta metav1.ObjectMeta, expected metav1.ObjectMeta) bool {
	return meta.Name == expected.Name && meta.Namespace == expected.Namespace && validDatabaseScanUID(meta.UID) && meta.DeletionTimestamp == nil && databaseScanExpectedLabels(meta.Labels, expected.Labels)
}
func (owner *DatabaseScanOwner) ensureScanNamespace(ctx context.Context, claim sqlc.VulnerabilityDatabaseScanClaim) error {
	namespace := corev1.Namespace{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"}, ObjectMeta: metav1.ObjectMeta{Name: databasejob.ScanNamespace, Labels: map[string]string{"app.kubernetes.io/managed-by": "astronomer", "pod-security.kubernetes.io/enforce": "privileged"}}}
	payload, err := owner.ensureOwnedObject(ctx, claim, "/api/v1/namespaces", "/api/v1/namespaces/"+databasejob.ScanNamespace, namespace, false)
	if err != nil {
		return err
	}
	defer clear(payload)
	var current corev1.Namespace
	if jsoncontract.Decode(payload, &current) != nil || current.Kind != "Namespace" || current.APIVersion != "v1" || !ownedDatabaseObjectMetadata(current.ObjectMeta, namespace.ObjectMeta) || current.Status.Phase == corev1.NamespaceTerminating {
		return errors.New("owned database scan namespace differs")
	}
	return nil
}
func (owner *DatabaseScanOwner) ensureScanMaterial(ctx context.Context, claim sqlc.VulnerabilityDatabaseScanClaim, readOnly bool) (types.UID, error) {
	members, _, _, err := owner.material.Data(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		for _, member := range members {
			clear(member)
		}
	}()
	immutable := true
	expected := corev1.Secret{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}, ObjectMeta: metav1.ObjectMeta{Name: "database-scan-" + claim.ID.String() + "-authority", Namespace: databasejob.ScanNamespace, Labels: map[string]string{"app.kubernetes.io/managed-by": "astronomer", "astronomer.io/database-claim": claim.ID.String()}}, Immutable: &immutable, Type: corev1.SecretTypeOpaque, Data: members}
	collection := "/api/v1/namespaces/" + databasejob.ScanNamespace + "/secrets"
	payload, err := owner.ensureOwnedObject(ctx, claim, collection, collection+"/"+expected.Name, expected, readOnly)
	if err != nil {
		return "", err
	}
	defer clear(payload)
	var current corev1.Secret
	if jsoncontract.Decode(payload, &current) != nil {
		return "", errors.New("owned database scan material readback invalid")
	}
	defer func() {
		for _, member := range current.Data {
			clear(member)
		}
	}()
	if current.Kind != "Secret" || current.APIVersion != "v1" || !ownedDatabaseObjectMetadata(current.ObjectMeta, expected.ObjectMeta) || current.Immutable == nil || !*current.Immutable || current.Type != expected.Type || len(current.StringData) != 0 || !reflect.DeepEqual(current.Data, members) {
		return "", errors.New("owned database scan material changed")
	}
	if claim.AuthoritySecretUid.Valid && current.UID != types.UID(uuid.UUID(claim.AuthoritySecretUid.Bytes).String()) {
		return "", errors.New("owned database scan material UID changed")
	}
	if err := owner.recordScanResourceUID(ctx, claim, collection+"/"+expected.Name, current.UID, readOnly); err != nil {
		return "", err
	}
	return current.UID, owner.revalidateScanMaterial(ctx)
}
func (owner *DatabaseScanOwner) ensureScanVolume(ctx context.Context, claim sqlc.VulnerabilityDatabaseScanClaim, expected *corev1.PersistentVolumeClaim, readOnly bool) (types.UID, error) {
	value := expected.DeepCopy()
	value.APIVersion = "v1"
	value.Kind = "PersistentVolumeClaim"
	collection := "/api/v1/namespaces/" + databasejob.ScanNamespace + "/persistentvolumeclaims"
	payload, err := owner.ensureOwnedObject(ctx, claim, collection, collection+"/"+url.PathEscape(value.Name), value, readOnly)
	if err != nil {
		return "", err
	}
	defer clear(payload)
	var current corev1.PersistentVolumeClaim
	if jsoncontract.Decode(payload, &current) != nil || current.Kind != value.Kind || current.APIVersion != value.APIVersion || !ownedDatabaseObjectMetadata(current.ObjectMeta, value.ObjectMeta) || !reflect.DeepEqual(current.Spec.AccessModes, value.Spec.AccessModes) || !reflect.DeepEqual(current.Spec.Resources, value.Spec.Resources) || current.Spec.DataSource != nil || current.Spec.DataSourceRef != nil || current.Spec.Selector != nil || current.Spec.VolumeMode != nil && *current.Spec.VolumeMode != corev1.PersistentVolumeFilesystem {
		return "", errors.New("owned database scan volume claim differs")
	}
	if claim.VolumeClaimUid.Valid && current.UID != types.UID(uuid.UUID(claim.VolumeClaimUid.Bytes).String()) {
		return "", errors.New("owned database scan volume UID changed")
	}
	if err := owner.recordScanResourceUID(ctx, claim, collection+"/"+value.Name, current.UID, readOnly); err != nil {
		return "", err
	}
	return current.UID, nil
}
func (owner *DatabaseScanOwner) ensureDenyPolicy(ctx context.Context, claim sqlc.VulnerabilityDatabaseScanClaim, expected *networkv1.NetworkPolicy, scanLabels map[string]string, readOnly bool) (types.UID, error) {
	value := expected.DeepCopy()
	value.APIVersion = "networking.k8s.io/v1"
	value.Kind = "NetworkPolicy"
	collection := "/apis/networking.k8s.io/v1/namespaces/" + databasejob.ScanNamespace + "/networkpolicies"
	payload, err := owner.ensureOwnedObject(ctx, claim, collection, collection+"/"+url.PathEscape(value.Name), value, readOnly)
	if err != nil {
		return "", err
	}
	defer clear(payload)
	var current networkv1.NetworkPolicy
	if jsoncontract.Decode(payload, &current) != nil || current.Kind != value.Kind || current.APIVersion != value.APIVersion || !ownedDatabaseObjectMetadata(current.ObjectMeta, value.ObjectMeta) || !reflect.DeepEqual(current.Spec, value.Spec) {
		return "", errors.New("owned database scan egress deny policy differs")
	}
	if claim.DenyEgressPolicyUid.Valid && current.UID != types.UID(uuid.UUID(claim.DenyEgressPolicyUid.Bytes).String()) {
		return "", errors.New("owned database scan egress policy UID changed")
	}
	// NetworkPolicy permissions combine by union. A second selecting policy with
	// any egress rule would defeat a deny-only policy and must fail before scan.
	scoped, err := newDatabaseScanScopedRequester(owner.requester, claim.ClusterID, []string{collection + "?limit=65"}, nil)
	if err != nil {
		return "", err
	}
	listBytes, err := databaseScanGetBytes(ctx, scoped, claim.ClusterID, collection+"?limit=65", databaseScanMaximumAPIBytes)
	if err != nil {
		return "", err
	}
	defer clear(listBytes)
	var list networkv1.NetworkPolicyList
	if jsoncontract.Decode(listBytes, &list) != nil || list.Kind != "NetworkPolicyList" || list.APIVersion != "networking.k8s.io/v1" || list.Continue != "" || len(list.Items) > 64 {
		return "", errors.New("owned database egress policy census unavailable")
	}
	found := false
	for _, policy := range list.Items {
		if policy.UID == current.UID {
			found = true
		}
		if policy.Namespace != databasejob.ScanNamespace || policy.DeletionTimestamp != nil {
			return "", errors.New("owned database egress policy census changed")
		}
		selector, err := metav1.LabelSelectorAsSelector(&policy.Spec.PodSelector)
		if err != nil {
			return "", errors.New("owned database egress selector invalid")
		}
		if selector.Matches(labels.Set(scanLabels)) && len(policy.Spec.Egress) != 0 {
			return "", errors.New("owned database selected Pod has another egress allowance")
		}
	}
	if !found {
		return "", errors.New("owned database egress deny policy missing from census")
	}
	if err := owner.recordScanResourceUID(ctx, claim, collection+"/"+value.Name, current.UID, readOnly); err != nil {
		return "", err
	}
	return current.UID, nil
}

func (owner *DatabaseScanOwner) ensureScanImagePullSecret(ctx context.Context, claim sqlc.VulnerabilityDatabaseScanClaim, readOnly bool) (types.UID, error) {
	if owner.kubeletPull == nil {
		if claim.ImagePullSecretUid.Valid {
			return "", errors.New("owned database Job pull configuration changed")
		}
		return "", nil
	}
	material, err := owner.kubeletPull.Data(ctx)
	if err != nil {
		return "", err
	}
	defer clear(material)
	immutable := true
	expected := corev1.Secret{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}, ObjectMeta: metav1.ObjectMeta{Name: owner.claimPullSecretName(claim.ID), Namespace: databasejob.ScanNamespace, Labels: map[string]string{"app.kubernetes.io/managed-by": "astronomer", "astronomer.io/database-claim": claim.ID.String()}}, Immutable: &immutable, Type: corev1.SecretTypeDockerConfigJson, Data: map[string][]byte{corev1.DockerConfigJsonKey: material}}
	collection := "/api/v1/namespaces/" + databasejob.ScanNamespace + "/secrets"
	payload, err := owner.ensureOwnedObject(ctx, claim, collection, collection+"/"+expected.Name, expected, readOnly)
	if err != nil {
		return "", err
	}
	defer clear(payload)
	var current corev1.Secret
	if jsoncontract.Decode(payload, &current) != nil {
		return "", errors.New("owned database Job pull Secret invalid")
	}
	defer func() {
		for _, value := range current.Data {
			clear(value)
		}
	}()
	if !ownedDatabaseObjectMetadata(current.ObjectMeta, expected.ObjectMeta) || current.Type != expected.Type || current.Immutable == nil || !*current.Immutable || len(current.StringData) != 0 || !reflect.DeepEqual(current.Data, expected.Data) {
		return "", errors.New("owned database Job pull Secret changed")
	}
	if claim.ImagePullSecretUid.Valid && current.UID != types.UID(uuid.UUID(claim.ImagePullSecretUid.Bytes).String()) {
		return "", errors.New("owned database Job pull Secret UID changed")
	}
	if err := owner.recordScanResourceUID(ctx, claim, collection+"/"+expected.Name, current.UID, readOnly); err != nil {
		return "", err
	}
	return current.UID, owner.kubeletPull.Revalidate(ctx)
}
