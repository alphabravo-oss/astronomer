package charlie

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OnboardingTransaction interface {
	GetPlatformConfig(context.Context) (sqlc.PlatformConfiguration, error)
	GetCharlieConnectionByPackageID(context.Context, string) (sqlc.CharlieConnection, error)
	CreateCharlieConnection(context.Context, sqlc.CreateCharlieConnectionParams) (sqlc.CharlieConnection, error)
	AdvanceCharlieOnboardingState(context.Context, sqlc.AdvanceCharlieOnboardingStateParams) (sqlc.CharlieConnection, error)
	GetUserByUsername(context.Context, string) (sqlc.User, error)
	CreateServiceUser(context.Context, sqlc.CreateServiceUserParams) (sqlc.User, error)
	GetCharlieAutomationRole(context.Context) (sqlc.GlobalRole, error)
	EnsureCharlieAutomationBinding(context.Context, sqlc.EnsureCharlieAutomationBindingParams) (sqlc.GlobalRoleBinding, error)
	CreateCharlieTriggerRule(context.Context, sqlc.CreateCharlieTriggerRuleParams) (sqlc.CharlieTriggerRule, error)
}

type OnboardingInstaller interface {
	PrepareNamespace(context.Context, uuid.UUID) (func(context.Context) error, error)
	Install(context.Context, AgentInstallSpec) (AgentInstallReceipt, error)
}

type OnboardingTransactionStore interface {
	WithinOnboardingTransaction(context.Context, func(OnboardingTransaction) error) error
}

type PGOnboardingTransactionStore struct{ Pool *pgxpool.Pool }

type onboardingFailure struct {
	code  string
	cause error
}

func (e *onboardingFailure) Error() string { return "Charlie onboarding failed" }
func (e *onboardingFailure) Unwrap() error { return e.cause }

func failOnboarding(code string, err error) error {
	return &onboardingFailure{code: code, cause: err}
}

// OnboardingFailureCode returns a bounded, credential-free operational code
// suitable for logs and metrics. Detailed Kubernetes/database errors remain
// inside the process and are never rendered to an API client or audit payload.
func OnboardingFailureCode(err error) string {
	var failure *onboardingFailure
	if errors.As(err, &failure) {
		return failure.code
	}
	return "onboarding.unknown"
}

func (s PGOnboardingTransactionStore) WithinOnboardingTransaction(ctx context.Context, callback func(OnboardingTransaction) error) error {
	if s.Pool == nil {
		return fmt.Errorf("Charlie onboarding database is unavailable")
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := callback(sqlc.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type OnboardingConsumer struct {
	Store           OnboardingTransactionStore
	Secrets         AgentSecretWriter
	Installer       OnboardingInstaller
	Encryptor       *auth.Encryptor
	BridgeServerDNS string
	MCPServerDNS    string
	Now             func() time.Time
	Auditor         AuthorityMutationAuditor
}

func (c *OnboardingConsumer) Consume(ctx context.Context, validated ValidatedOnboarding, actorID uuid.UUID) (OnboardingStatus, error) {
	if c == nil || c.Store == nil || c.Secrets == nil || c.Encryptor == nil || c.Auditor == nil {
		return OnboardingStatus{}, failOnboarding("onboarding.dependencies_unavailable", fmt.Errorf("Charlie onboarding dependencies are unavailable"))
	}
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now().UTC()
	}
	if err := requireAuthorityMutationAudit(ctx, c.Auditor, AuthorityMutationAudit{
		Action: "charlie.connection.onboarding_validated", ResourceType: "charlie_connection", ResourceID: digestBytes([]byte(validated.PackageID)), ActorID: actorID,
	}); err != nil {
		return OnboardingStatus{}, failOnboarding("onboarding.audit_unavailable", fmt.Errorf("Charlie onboarding audit is unavailable"))
	}
	var result OnboardingStatus
	var rollbacks []func(context.Context) error
	err := c.Store.WithinOnboardingTransaction(ctx, func(tx OnboardingTransaction) error {
		existing, err := tx.GetCharlieConnectionByPackageID(ctx, validated.PackageID)
		if err == nil {
			result = validated.SafeStatus(existing.OnboardingState, true)
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return failOnboarding("onboarding.package_lookup_failed", err)
		}
		platform, err := tx.GetPlatformConfig(ctx)
		if err != nil {
			return failOnboarding("onboarding.installation_identity_failed", err)
		}
		trust, err := GenerateLocalTrust(c.Encryptor, LocalTrustConfig{
			InstallationID: platform.InstanceID.String(), BridgeServerDNS: c.BridgeServerDNS,
			MCPServerDNS: c.MCPServerDNS, Now: now,
		})
		if err != nil {
			return failOnboarding("onboarding.local_trust_failed", err)
		}
		if c.Installer != nil {
			namespaceRollback, prepareErr := c.Installer.PrepareNamespace(ctx, platform.InstanceID)
			if prepareErr != nil {
				return failOnboarding("onboarding.namespace_prepare_failed", prepareErr)
			}
			rollbacks = append(rollbacks, namespaceRollback)
		}
		secretName := "charlie-agent-bootstrap-" + safeSecretSuffix(validated.PackageID)
		enrollmentExpiresAt, artifactExpiresAt, expiryErr := onboardingCredentialExpiries(validated.Package)
		if expiryErr != nil {
			return failOnboarding("onboarding.credential_expiry_invalid", expiryErr)
		}
		certificateExpiresAt, expiryErr := onboardingCertificateExpiry(validated.Package, trust.ExpiresAt)
		if expiryErr != nil {
			return failOnboarding("onboarding.certificate_expiry_invalid", expiryErr)
		}
		connection, err := tx.CreateCharlieConnection(ctx, sqlc.CreateCharlieConnectionParams{
			InstallationID: platform.InstanceID, ProductID: string(validated.Package.ProductId), ProductSlug: validated.Package.ProductSlug, DeploymentID: string(validated.Package.DeploymentId),
			RouteID: string(validated.Package.Route.RouteId), CentralUrl: validated.Package.Central.BaseUrl,
			CentralCaFingerprint: validated.Package.Central.CertificateSha256,
			SigningKeyID:         string(validated.Package.Signing.KeyId), SigningKeyFingerprint: validated.Package.Signing.PublicKeySha256,
			OnboardingSchemaVersion: validated.Package.Schema, CentralApiVersion: validated.Package.CentralApiVersion,
			AgentProtocolVersion: contract.AgentProtocolVersion, ChartReference: validated.Package.Artifact.Chart, ChartVersion: contract.AgentChartVersion,
			ChartDigest: validated.Package.Artifact.ChartDigest, ImageReference: validated.Package.Artifact.Image, ImageDigest: validated.Package.Artifact.ManifestDigest,
			LogicalAgentID: string(validated.Package.LogicalAgentId), ReplicaCount: int32(validated.Package.ReplicaCount), BridgeServiceName: c.BridgeServerDNS,
			McpServiceName: c.MCPServerDNS, LocalTrustMaterialEncrypted: trust.EncryptedLocalTrust,
			AgentSecretName: secretName, OnboardingPackageID: validated.PackageID,
			OnboardingPackageDigest: validated.RawDigest, OnboardingState: "secrets_pending",
			OnboardingPackageExpiresAt:     validated.Package.ExpiresAt.UTC(),
			EnrollmentCredentialsExpiresAt: enrollmentExpiresAt,
			ArtifactCredentialExpiresAt:    artifactExpiresAt,
			CertificateExpiresAt:           certificateExpiresAt,
			DisclosureDigest:               CapabilityDisclosureDigest(), HealthState: "inactive",
			CreatedByID: pgtype.UUID{Bytes: actorID, Valid: true},
		})
		if err != nil {
			return failOnboarding("onboarding.connection_create_failed", err)
		}
		automationIdentity, err := EnsureAutomationIdentity(ctx, tx)
		if err != nil {
			return failOnboarding("onboarding.automation_identity_failed", err)
		}
		if err := EnsureDefaultTriggerRules(ctx, tx, connection.ID, automationIdentity.ID, actorID); err != nil {
			return failOnboarding("onboarding.trigger_defaults_failed", err)
		}
		receipt, err := c.Secrets.WriteAgentSecret(ctx, AgentSecretBundle{
			Name: secretName, InstallationID: platform.InstanceID.String(), OnboardingPackage: validated.RawPackage,
			ArtifactPullCredential: validated.ArtifactCredential, CACertificatePEM: trust.Agent.CACertificatePEM,
			BridgeServerCertificate: trust.Agent.BridgeServerCertificate, BridgeServerPrivateKey: trust.Agent.BridgeServerPrivateKey,
			MCPClientCertificate: trust.Agent.MCPClientCertificate, MCPClientPrivateKey: trust.Agent.MCPClientPrivateKey,
		})
		if err != nil {
			return failOnboarding("onboarding.secret_materialization_failed", err)
		}
		rollbacks = append(rollbacks, receipt.Rollback)
		if c.Installer != nil {
			disclosureDigest := CapabilityDisclosureDigest()
			installSpec := AgentInstallSpec{
				InstallationID: platform.InstanceID, ConnectionID: connection.ID,
				LogicalAgentID: string(validated.Package.LogicalAgentId), DeploymentID: string(validated.Package.DeploymentId), EnvironmentID: string(validated.Package.EnvironmentId), TenantID: string(validated.Package.TenantId),
				OnboardingPackageID: validated.PackageID,
				CentralURL:          validated.Package.Central.BaseUrl, CentralCAPEM: validated.Package.Central.CaBundlePem,
				ChartReference: validated.Package.Artifact.Chart, ChartVersion: contract.AgentChartVersion, ChartDigest: validated.Package.Artifact.ChartDigest,
				ImageReference: validated.Package.Artifact.Image, ImageDigest: validated.Package.Artifact.ManifestDigest,
				OnboardingPackage: validated.RawPackage, ReplicaCount: validated.Package.ReplicaCount, ArtifactCredential: validated.ArtifactCredential,
				SecretPrefix: secretName, DisclosureDigest: disclosureDigest, SecretIntegrityHMAC: receipt.IntegrityHMAC,
				ActionSigningPublicKey: validated.SigningPublicKey, ActionSigningKeyFingerprint: validated.Package.Signing.PublicKeySha256,
				Trust: trust,
			}
			// A replacement package revokes the old registry credential before
			// this transaction begins. Remove owner-bound repository credentials
			// for earlier package generations before Argo resolves the OCI source,
			// otherwise it can nondeterministically select a revoked credential.
			if pruner, ok := c.Installer.(supersededAgentMaterialPruner); ok {
				if pruneErr := pruner.PruneSupersededRepositories(ctx, installSpec); pruneErr != nil {
					return failOnboarding("onboarding.repository_prune_failed", pruneErr)
				}
			}
			installReceipt, installErr := c.Installer.Install(ctx, installSpec)
			if installErr != nil {
				return failOnboarding("onboarding.agent_install_failed", installErr)
			}
			rollbacks = append(rollbacks, installReceipt.Rollback)
		}
		connection, err = tx.AdvanceCharlieOnboardingState(ctx, sqlc.AdvanceCharlieOnboardingStateParams{
			ID: connection.ID, ExpectedState: "secrets_pending", NextState: "secrets_written",
			AgentSecretHmac: receipt.IntegrityHMAC,
		})
		if err != nil {
			return failOnboarding("onboarding.state_record_failed", err)
		}
		_, err = tx.AdvanceCharlieOnboardingState(ctx, sqlc.AdvanceCharlieOnboardingStateParams{
			ID: connection.ID, ExpectedState: "secrets_written", NextState: "consumed",
			AgentSecretHmac: receipt.IntegrityHMAC,
		})
		if err != nil {
			return failOnboarding("onboarding.state_consume_failed", err)
		}
		result = validated.SafeStatus("consumed", false)
		return nil
	})
	if err != nil {
		for index := len(rollbacks) - 1; index >= 0; index-- {
			if rollbackErr := rollbacks[index](ctx); rollbackErr != nil {
				return OnboardingStatus{}, failOnboarding("onboarding.rollback_failed", rollbackErr)
			}
		}
		return OnboardingStatus{}, err
	}
	return result, nil
}

func safeSecretSuffix(packageID string) string {
	value := strings.ToLower(packageID)
	value = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, value)
	value = strings.Trim(value, "-")
	if len(value) > 40 {
		value = value[:40]
	}
	return value
}

func onboardingCredentialExpiries(pkg contract.OnboardingPackage) (time.Time, time.Time, error) {
	var enrollment, artifact time.Time
	for _, credential := range pkg.Credentials {
		switch credential.Purpose {
		case contract.CredentialPurposeAgentEnrollment:
			if enrollment.IsZero() || credential.ExpiresAt.Before(enrollment) {
				enrollment = credential.ExpiresAt.UTC()
			}
		case contract.CredentialPurposeArtifactPull:
			artifact = credential.ExpiresAt.UTC()
		}
	}
	if enrollment.IsZero() || artifact.IsZero() {
		return time.Time{}, time.Time{}, fmt.Errorf("Charlie onboarding credential expiry metadata is incomplete")
	}
	return enrollment, artifact, nil
}

func onboardingCertificateExpiry(pkg contract.OnboardingPackage, localExpiry time.Time) (time.Time, error) {
	certificates, err := parseCertificateBundle(pkg.Central.CaBundlePem)
	if err != nil || localExpiry.IsZero() {
		return time.Time{}, fmt.Errorf("Charlie certificate expiry metadata is incomplete")
	}
	expiresAt := localExpiry.UTC()
	for _, certificate := range certificates {
		if certificate.NotAfter.Before(expiresAt) {
			expiresAt = certificate.NotAfter.UTC()
		}
	}
	return expiresAt, nil
}
