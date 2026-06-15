package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/strutil"
)

// Bootstrap env vars are checked when the database has zero users on boot.
// Operator-supplied values beat the defaults. The password var matches
// Rancher's CATTLE_BOOTSTRAP_PASSWORD convention.
const (
	bootstrapPasswordEnv = "ASTRONOMER_BOOTSTRAP_PASSWORD"
	bootstrapUsernameEnv = "ASTRONOMER_BOOTSTRAP_USERNAME"
	bootstrapEmailEnv    = "ASTRONOMER_BOOTSTRAP_EMAIL"
)

// EnsurePlatformConfigQuerier is the slice of sqlc Queries that the
// platform-config seed flow needs.
type EnsurePlatformConfigQuerier interface {
	GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)
	UpsertPlatformConfig(ctx context.Context, arg sqlc.UpsertPlatformConfigParams) (sqlc.PlatformConfiguration, error)
}

// EnsurePlatformConfig seeds platform_configuration.server_url from the
// supplied serverURL on first boot if the row doesn't yet exist, and back-
// fills server_url on existing rows that have an empty value. Operators can
// always override the URL afterwards through /dashboard/settings, so this is
// purely a default for fresh installs.
//
// The self-management loop in internal/server/self_manage_argocd.go won't
// create the astronomer-self-manage Argo Application until server_url is
// populated; seeding it here means a `helm install` with no manual setup
// reaches the self-managed state automatically.
func EnsurePlatformConfig(ctx context.Context, q EnsurePlatformConfigQuerier, serverURL, platformName string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	serverURL = strings.TrimSpace(serverURL)
	if serverURL == "" {
		logger.Debug("platform config: no server_url provided, skipping seed")
		return nil
	}
	if platformName == "" {
		platformName = "Astronomer"
	}

	existing, err := q.GetPlatformConfig(ctx)
	if err != nil && err != pgx.ErrNoRows {
		return fmt.Errorf("get platform config: %w", err)
	}

	// Existing row with a populated server_url: leave it alone so operator
	// edits in /dashboard/settings aren't clobbered on every restart.
	if err == nil && strings.TrimSpace(existing.ServerUrl) != "" {
		return nil
	}

	target := sqlc.UpsertPlatformConfigParams{
		ServerUrl:        serverURL,
		PlatformName:     platformName,
		TelemetryEnabled: true,
		BootstrappedAt:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}
	// Preserve an existing instance_id across upserts so observability tags
	// stay consistent.
	if err == nil {
		target.PlatformName = strutil.FirstNonBlankTrimmed(existing.PlatformName, platformName)
		target.TelemetryEnabled = existing.TelemetryEnabled
		target.BootstrappedAt = existing.BootstrappedAt
		target.InstanceID = existing.InstanceID
	}

	if _, err := q.UpsertPlatformConfig(ctx, target); err != nil {
		return fmt.Errorf("upsert platform config: %w", err)
	}
	logger.Info("platform config seeded", "server_url", serverURL, "platform_name", target.PlatformName)
	return nil
}

// EnsureAdminQuerier is the slice of sqlc Queries that the bootstrap admin
// flow needs. Defined as an interface so tests can plug in a fake.
type EnsureAdminQuerier interface {
	CountUsers(ctx context.Context) (int64, error)
	CreateBootstrapAdmin(ctx context.Context, arg sqlc.CreateBootstrapAdminParams) (sqlc.User, error)
}

// EnsureBootstrapAdmin creates an admin user the first time the server boots
// against an empty users table. The password comes from
// ASTRONOMER_BOOTSTRAP_PASSWORD when set, otherwise a random 24-character
// URL-safe value is generated. Chart installs persist that password in the
// bootstrap Secret so operators can retrieve it with kubectl.
//
// On subsequent boots (when users already exist) this is a no-op.
func EnsureBootstrapAdmin(ctx context.Context, q EnsureAdminQuerier, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	count, err := q.CountUsers(ctx)
	if err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		logger.Debug("bootstrap admin: skipped, users already exist", "count", count)
		return nil
	}

	password := strings.TrimSpace(os.Getenv(bootstrapPasswordEnv))
	username := strutil.FirstNonBlankTrimmed(os.Getenv(bootstrapUsernameEnv), "admin")
	email := strutil.FirstNonBlankTrimmed(os.Getenv(bootstrapEmailEnv), "admin@astronomer.local")
	generated := false
	if password == "" {
		pw, err := generateBootstrapPassword()
		if err != nil {
			return fmt.Errorf("generate bootstrap password: %w", err)
		}
		password = pw
		generated = true
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash bootstrap password: %w", err)
	}

	user, err := q.CreateBootstrapAdmin(ctx, sqlc.CreateBootstrapAdminParams{
		Email:     email,
		Username:  username,
		FirstName: "Admin",
		LastName:  "",
		Password:  string(hashed),
	})
	if err != nil {
		return fmt.Errorf("create bootstrap admin: %w", err)
	}

	// Surface the credentials prominently. The log line is intentionally
	// formatted on a single block so it's easy to grep with `kubectl logs |
	// grep BOOTSTRAP`. Only the auto-generated case prints the password;
	// when ASTRONOMER_BOOTSTRAP_PASSWORD was supplied, the operator already
	// knows it.
	logger.Warn(
		"==================== BOOTSTRAP ADMIN CREATED ====================",
		"username", user.Username,
		"email", user.Email,
		"password_source", passwordSourceLabel(generated),
	)
	if generated {
		logger.Warn(
			"Use this generated password to sign in. The Helm chart also stores it "+
				"in the bootstrap Secret for operator retrieval.",
			"password", password,
		)
	}
	logger.Warn("=================================================================")
	return nil
}

func generateBootstrapPassword() (string, error) {
	buf := make([]byte, 18) // 18 bytes -> 24 base64 chars
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	// URL-safe base64 without padding; replace characters that are awkward
	// to type so an operator can read it back from a terminal cleanly.
	s := base64.RawURLEncoding.EncodeToString(buf)
	s = strings.NewReplacer("-", "x", "_", "y").Replace(s)
	return s, nil
}

func passwordSourceLabel(generated bool) string {
	if generated {
		return "auto-generated"
	}
	return bootstrapPasswordEnv
}
