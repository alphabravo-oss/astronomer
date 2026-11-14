// Package provisioning adapts shared Identity provisioning to product-owned
// credentials. It does not enable SSO or contact an issuer at server startup.
package provisioning

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"

	"github.com/alphabravo-oss/identity/pkg/provision"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	typedcore "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

var (
	ErrInvalid     = errors.New("invalid Identity credential scope or material")
	ErrConflict    = errors.New("Identity credential already exists with different ownership or material")
	ErrUnavailable = errors.New("Identity credential storage unavailable")
	ErrNotFound    = errors.New("Identity credential not found; registration recovery required")
)

// Scope identifies one immutable credential generation. Callers must authorize
// the project and installation before constructing a store. A generation change
// is an explicit rotation, never an automatic response to missing credentials.
type Scope struct {
	Issuer         string
	ProjectID      uuid.UUID
	InstallationID uuid.UUID
	Generation     uint64
}

// ProjectName prevents shared Identity's project-keyed Put callback from
// colliding between installations. Rotation keeps the same upstream project so
// project roles and grants are not accidentally discarded.
func (s Scope) ProjectName() string {
	return "astronomer-" + s.ProjectID.String() + "-" + s.InstallationID.String()
}

func (s Scope) valid() bool {
	u, err := url.Parse(s.Issuer)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" &&
		len(s.Issuer) <= 2048 && s.ProjectID != uuid.Nil && s.InstallationID != uuid.Nil && s.Generation > 0
}

type credentialDocument struct {
	Version   int
	Namespace string
	Scope     Scope
	Result    provision.Result
}

// SecretStore persists application-encrypted material in an immutable Kubernetes
// Secret. Labels are an inventory aid; authenticated ownership is inside the
// encrypted document. Copying ciphertext into another scope cannot transfer it.
// The keyring must be included in backup/recovery and retained across rotation.
type SecretStore struct {
	secrets   typedcore.SecretInterface
	cipher    *crypto.Encryptor
	namespace string
	scope     Scope
	name      string
}

var _ provision.SecretStore = (*SecretStore)(nil)

func NewSecretStore(core typedcore.CoreV1Interface, namespace string, scope Scope, cipher *crypto.Encryptor) (*SecretStore, error) {
	if core == nil || cipher == nil || len(validation.IsDNS1123Label(namespace)) != 0 || !scope.valid() {
		return nil, ErrInvalid
	}
	encoded, err := json.Marshal(scope)
	if err != nil {
		return nil, ErrInvalid
	}
	digest := sha256.Sum256(encoded)
	return &SecretStore{secrets: core.Secrets(namespace), cipher: cipher, namespace: namespace, scope: scope,
		name: "identity-client-" + hex.EncodeToString(digest[:20])}, nil
}

// Name is safe to put in an operation receipt. Results and plaintext credentials
// must never be included in logs, audit metadata, Helm values or API responses.
func (s *SecretStore) Name() string { return s.name }

func validResult(r provision.Result) bool {
	for _, value := range []string{r.OrgID, r.ProjectID, r.AppID, r.ClientID, r.ClientSecret} {
		if value == "" || len(value) > 32768 {
			return false
		}
	}
	return true
}

func durableResult(r provision.Result) provision.Result {
	r.Created, r.Updated = false, false
	return r
}

// Put is create-only and idempotent. A conflicting credential is never replaced.
// On failure shared provision.Ensure returns its create-only Result; callers must
// retry Put with that Result, not call Ensure again and lose the secret.
func (s *SecretStore) Put(ctx context.Context, project string, result provision.Result) error {
	if project != s.scope.ProjectName() || !validResult(result) {
		return ErrInvalid
	}
	result = durableResult(result)
	plain, err := json.Marshal(credentialDocument{Version: 1, Namespace: s.namespace, Scope: s.scope, Result: result})
	if err != nil || len(plain) > 128*1024 {
		return ErrInvalid
	}
	sealed, err := s.cipher.Encrypt(plain)
	if err != nil {
		return ErrUnavailable
	}
	encoded, err := json.Marshal(sealed)
	if err != nil {
		return ErrUnavailable
	}
	immutable := true
	_, err = s.secrets.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: s.name, Namespace: s.namespace, Labels: map[string]string{
			"app.kubernetes.io/managed-by":        "astronomer",
			"astronomer.io/identity-installation": s.scope.InstallationID.String(),
			"astronomer.io/identity-project":      s.scope.ProjectID.String(),
		}},
		Type: corev1.SecretTypeOpaque, Immutable: &immutable, Data: map[string][]byte{"credential.enc": encoded},
	}, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return ErrUnavailable
	}
	existing, err := s.Get(ctx)
	if err != nil {
		return err
	}
	left, _ := json.Marshal(existing)
	right, _ := json.Marshal(result)
	if subtle.ConstantTimeCompare(left, right) != 1 {
		return ErrConflict
	}
	return nil
}

// Get never falls back to a plaintext Secret or an upstream client lookup.
// Missing material requires explicit recovery; it is not permission to create a
// replacement application. Successful reads clear transient Created/Updated flags.
func (s *SecretStore) Get(ctx context.Context) (provision.Result, error) {
	secret, err := s.secrets.Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return provision.Result{}, ErrNotFound
	}
	if err != nil {
		return provision.Result{}, ErrUnavailable
	}
	return s.decode(secret)
}

// Delete removes only authenticated, owned material with a Kubernetes version
// precondition. The workflow must first revoke the upstream application and
// confirm consumers no longer need this generation. It must not call Delete as
// rollback for a failed Put, which may have durably succeeded despite a timeout.
func (s *SecretStore) Delete(ctx context.Context) error {
	secret, err := s.secrets.Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return ErrUnavailable
	}
	if _, err := s.decode(secret); err != nil {
		return err
	}
	err = s.secrets.Delete(ctx, s.name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{
		UID: &secret.UID, ResourceVersion: &secret.ResourceVersion,
	}})
	if apierrors.IsConflict(err) {
		return ErrConflict
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return ErrUnavailable
	}
	return nil
}

func (s *SecretStore) decode(secret *corev1.Secret) (provision.Result, error) {
	if secret.Name != s.name || secret.Namespace != s.namespace || secret.Type != corev1.SecretTypeOpaque ||
		secret.Immutable == nil || !*secret.Immutable || len(secret.Data) != 1 ||
		secret.Labels["app.kubernetes.io/managed-by"] != "astronomer" ||
		secret.Labels["astronomer.io/identity-installation"] != s.scope.InstallationID.String() ||
		secret.Labels["astronomer.io/identity-project"] != s.scope.ProjectID.String() {
		return provision.Result{}, ErrConflict
	}
	var sealed crypto.Sealed
	if len(secret.Data["credential.enc"]) > 256*1024 || json.Unmarshal(secret.Data["credential.enc"], &sealed) != nil || sealed.IsZero() {
		return provision.Result{}, ErrUnavailable
	}
	plain, err := s.cipher.Decrypt(sealed)
	if err != nil {
		return provision.Result{}, ErrUnavailable
	}
	var doc credentialDocument
	if json.Unmarshal(plain, &doc) != nil || doc.Version != 1 || doc.Namespace != s.namespace || doc.Scope != s.scope {
		return provision.Result{}, ErrConflict
	}
	if !validResult(doc.Result) {
		return provision.Result{}, ErrUnavailable
	}
	return durableResult(doc.Result), nil
}
