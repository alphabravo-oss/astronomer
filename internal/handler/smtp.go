package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/email"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// callerUsername returns the authenticated user's username (or
// "anonymous" when not authenticated). Used by the test-send path so
// the rendered email body identifies who clicked the button.
func callerUsername(r *http.Request) string {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || user == nil {
		return "anonymous"
	}
	if user.Username != "" {
		return user.Username
	}
	return user.ID
}

// SMTPQuerier is the database surface SMTPHandler needs. *sqlc.Queries
// satisfies this directly; tests pass a narrow fake.
type SMTPQuerier interface {
	GetSMTPSettings(ctx context.Context, id uuid.UUID) (sqlc.SmtpSettings, error)
	UpsertSMTPSettings(ctx context.Context, arg sqlc.UpsertSMTPSettingsParams) (sqlc.SmtpSettings, error)
	ListEmailMessages(ctx context.Context, arg sqlc.ListEmailMessagesParams) ([]sqlc.EmailMessage, error)
	CountEmailMessages(ctx context.Context) (int64, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
}

// SMTPTestSender is the surface used by the test-send endpoint. The
// production wiring passes a *email.Sender configured against a
// StaticSettingsProvider built from the request payload — so the
// admin can dry-run a candidate config WITHOUT first PUTting it.
type SMTPTestSender interface {
	Send(ctx context.Context, msg email.Message) error
}

// PasswordSentinelEncrypted is the placeholder returned in GET
// responses instead of the raw ciphertext. The dashboard PUT path
// echoes this value back when the admin didn't change the password,
// and the handler recognises it as "keep the existing password" so a
// fresh PUT doesn't accidentally blank the column.
const PasswordSentinelEncrypted = "<encrypted>"

// SMTPHandler owns /api/v1/admin/smtp/* and /api/v1/admin/emails/.
// Superuser-gated inside the handler so non-admins get a clean 403.
type SMTPHandler struct {
	queries   SMTPQuerier
	encryptor *auth.Encryptor
	branding  email.BrandingProvider
	// provider is the cache-invalidation hook into the Sender's
	// SettingsProvider. Optional — when nil, settings changes pick
	// up on the next TTL expiry rather than immediately.
	provider     *email.SQLSettingsProvider
	newTestSender func(cfg email.Settings) SMTPTestSender
	log          *slog.Logger
	audit        AuthAuditWriter
}

// NewSMTPHandler wires the production handler.
func NewSMTPHandler(queries SMTPQuerier, encryptor *auth.Encryptor, log *slog.Logger) *SMTPHandler {
	if log == nil {
		log = slog.Default()
	}
	return &SMTPHandler{
		queries:   queries,
		encryptor: encryptor,
		log:       log,
		newTestSender: func(cfg email.Settings) SMTPTestSender {
			return email.NewSender(email.StaticSettingsProvider{Cfg: cfg}, encryptor, log)
		},
	}
}

// SetBrandingProvider attaches the branding lookup used by the test-send
// template render path.
func (h *SMTPHandler) SetBrandingProvider(b email.BrandingProvider) { h.branding = b }

// SetSettingsProvider hooks the Sender's settings cache so a PUT
// invalidates the cached row immediately instead of waiting for the
// next TTL tick.
func (h *SMTPHandler) SetSettingsProvider(p *email.SQLSettingsProvider) { h.provider = p }

// SetAuditWriter wires the audit log writer.
func (h *SMTPHandler) SetAuditWriter(a AuthAuditWriter) { h.audit = a }

// SetTestSenderFactory is a test seam — production never calls it.
func (h *SMTPHandler) SetTestSenderFactory(f func(cfg email.Settings) SMTPTestSender) {
	if f != nil {
		h.newTestSender = f
	}
}

// smtpSettingsResponse mirrors the GET payload. password is always the
// sentinel — we NEVER leak the encrypted column over the wire.
type smtpSettingsResponse struct {
	Enabled        bool   `json:"enabled"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Username       string `json:"username"`
	Password       string `json:"password"` // sentinel
	FromAddress    string `json:"from_address"`
	FromName       string `json:"from_name"`
	AuthMechanism  string `json:"auth_mechanism"`
	Encryption     string `json:"encryption"`
	RequireTLS     bool   `json:"require_tls"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	UpdatedAt      string `json:"updated_at"`
	// PasswordConfigured surfaces whether a password is set at all so
	// the dashboard can render "(no password)" vs "<encrypted>" without
	// the sentinel leaking semantically.
	PasswordConfigured bool `json:"password_configured"`
}

// Get handles GET /api/v1/admin/smtp/.
//
// Superuser-gated. The password column is replaced with
// PasswordSentinelEncrypted regardless of whether one is actually
// configured (PasswordConfigured carries that bit).
func (h *SMTPHandler) Get(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	row, err := h.queries.GetSMTPSettings(r.Context(), email.SingletonSettingsID)
	if errors.Is(err, pgx.ErrNoRows) {
		// No row yet → return an "empty" payload so the dashboard
		// renders a fresh form. enabled=false is the safe default.
		RespondJSON(w, http.StatusOK, smtpSettingsResponse{
			Port:           587,
			AuthMechanism:  "plain",
			Encryption:     "starttls",
			RequireTLS:     true,
			TimeoutSeconds: 30,
			FromName:       "Astronomer",
			Password:       PasswordSentinelEncrypted,
		})
		return
	}
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "read_error", "Failed to read SMTP settings")
		return
	}
	RespondJSON(w, http.StatusOK, smtpSettingsResponse{
		Enabled:            row.Enabled,
		Host:               row.Host,
		Port:               int(row.Port),
		Username:           row.Username,
		Password:           PasswordSentinelEncrypted,
		PasswordConfigured: row.PasswordEncrypted != "",
		FromAddress:        row.FromAddress,
		FromName:           row.FromName,
		AuthMechanism:      row.AuthMechanism,
		Encryption:         row.Encryption,
		RequireTLS:         row.RequireTls,
		TimeoutSeconds:     int(row.TimeoutSeconds),
		UpdatedAt:          row.UpdatedAt.UTC().Format(time.RFC3339),
	})
}

// smtpSettingsUpdate is the PUT body. Password is OPTIONAL: when the
// admin leaves the field as the PasswordSentinelEncrypted string the
// existing ciphertext is preserved; any other value is re-encrypted
// and replaces it. This is how Rancher does it.
type smtpSettingsUpdate struct {
	Enabled        *bool   `json:"enabled"`
	Host           *string `json:"host"`
	Port           *int    `json:"port"`
	Username       *string `json:"username"`
	Password       *string `json:"password"`
	FromAddress    *string `json:"from_address"`
	FromName       *string `json:"from_name"`
	AuthMechanism  *string `json:"auth_mechanism"`
	Encryption     *string `json:"encryption"`
	RequireTLS     *bool   `json:"require_tls"`
	TimeoutSeconds *int    `json:"timeout_seconds"`
}

// Update handles PUT /api/v1/admin/smtp/.
func (h *SMTPHandler) Update(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	var req smtpSettingsUpdate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	// Pull the existing row (or the empty defaults) so the merge
	// below is straightforward.
	existing, err := h.queries.GetSMTPSettings(r.Context(), email.SingletonSettingsID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		RespondError(w, http.StatusInternalServerError, "read_error", "Failed to read SMTP settings")
		return
	}

	merged := h.mergeUpdate(existing, req)
	if vErr := h.validate(merged); vErr != "" {
		RespondError(w, http.StatusBadRequest, "validation_error", vErr)
		return
	}

	// Encrypt the password if the admin sent a fresh value (anything
	// other than the sentinel). Sentinel → keep existing ciphertext.
	encryptedPassword := existing.PasswordEncrypted
	if req.Password != nil && *req.Password != PasswordSentinelEncrypted {
		if *req.Password == "" {
			encryptedPassword = ""
		} else {
			if h.encryptor == nil {
				RespondError(w, http.StatusServiceUnavailable, "not_configured", "Encryptor is not configured; cannot store SMTP password")
				return
			}
			enc, err := h.encryptor.Encrypt(*req.Password)
			if err != nil {
				RespondError(w, http.StatusInternalServerError, "encrypt_error", "Failed to encrypt password")
				return
			}
			encryptedPassword = enc
		}
	}

	saved, err := h.queries.UpsertSMTPSettings(r.Context(), sqlc.UpsertSMTPSettingsParams{
		ID:                email.SingletonSettingsID,
		Enabled:           merged.Enabled,
		Host:              merged.Host,
		Port:              merged.Port,
		Username:          merged.Username,
		PasswordEncrypted: encryptedPassword,
		FromAddress:       merged.FromAddress,
		FromName:          merged.FromName,
		AuthMechanism:     merged.AuthMechanism,
		Encryption:        merged.Encryption,
		RequireTls:        merged.RequireTLS,
		TimeoutSeconds:    merged.TimeoutSeconds,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "write_error", "Failed to save SMTP settings")
		return
	}
	if h.provider != nil {
		h.provider.Invalidate()
	}

	// Audit row. We DON'T persist host/port/from into the audit
	// detail — that's already in smtp_settings and would only widen
	// the log surface. We DO persist the enabled flag because it
	// changes runtime behaviour for everyone.
	recordAudit(r, h.audit, "admin.smtp.update", "smtp", saved.ID.String(), "smtp_settings", map[string]any{
		"enabled":         saved.Enabled,
		"password_set":    saved.PasswordEncrypted != "",
		"auth_mechanism":  saved.AuthMechanism,
		"encryption":      saved.Encryption,
		"require_tls":     saved.RequireTls,
	})

	h.writeResponseFromRow(w, saved)
}

// rawSettings is the validated merged-update shape — values are
// already concrete (no pointers).
type rawSettings struct {
	Enabled        bool
	Host           string
	Port           int32
	Username       string
	FromAddress    string
	FromName       string
	AuthMechanism  string
	Encryption     string
	RequireTLS     bool
	TimeoutSeconds int32
}

func (h *SMTPHandler) mergeUpdate(existing sqlc.SmtpSettings, req smtpSettingsUpdate) rawSettings {
	out := rawSettings{
		Enabled:        existing.Enabled,
		Host:           existing.Host,
		Port:           existing.Port,
		Username:       existing.Username,
		FromAddress:    existing.FromAddress,
		FromName:       existing.FromName,
		AuthMechanism:  existing.AuthMechanism,
		Encryption:     existing.Encryption,
		RequireTLS:     existing.RequireTls,
		TimeoutSeconds: existing.TimeoutSeconds,
	}
	if out.Port == 0 {
		out.Port = 587
	}
	if out.AuthMechanism == "" {
		out.AuthMechanism = "plain"
	}
	if out.Encryption == "" {
		out.Encryption = "starttls"
	}
	if out.FromName == "" {
		out.FromName = "Astronomer"
	}
	if out.TimeoutSeconds == 0 {
		out.TimeoutSeconds = 30
	}
	if req.Enabled != nil {
		out.Enabled = *req.Enabled
	}
	if req.Host != nil {
		out.Host = strings.TrimSpace(*req.Host)
	}
	if req.Port != nil {
		out.Port = int32(*req.Port)
	}
	if req.Username != nil {
		out.Username = *req.Username
	}
	if req.FromAddress != nil {
		out.FromAddress = strings.TrimSpace(*req.FromAddress)
	}
	if req.FromName != nil {
		out.FromName = *req.FromName
	}
	if req.AuthMechanism != nil {
		out.AuthMechanism = strings.ToLower(strings.TrimSpace(*req.AuthMechanism))
	}
	if req.Encryption != nil {
		out.Encryption = strings.ToLower(strings.TrimSpace(*req.Encryption))
	}
	if req.RequireTLS != nil {
		out.RequireTLS = *req.RequireTLS
	}
	if req.TimeoutSeconds != nil {
		out.TimeoutSeconds = int32(*req.TimeoutSeconds)
	}
	return out
}

// validate returns "" on success or a single-sentence error message
// suitable for surfacing as the body of a 400.
func (h *SMTPHandler) validate(s rawSettings) string {
	if !s.Enabled {
		// When disabled, the rest of the fields don't have to be
		// valid — the operator may be staging a config they'll
		// finish later.
		return ""
	}
	if s.Host == "" {
		return "host is required"
	}
	if s.Port <= 0 || s.Port > 65535 {
		return "port must be 1..65535"
	}
	if s.FromAddress == "" {
		return "from_address is required"
	}
	if _, err := mail.ParseAddress(s.FromAddress); err != nil {
		return "from_address is not a valid email address"
	}
	switch s.AuthMechanism {
	case "plain", "login", "cram-md5", "none":
	default:
		return "auth_mechanism must be one of plain, login, cram-md5, none"
	}
	switch s.Encryption {
	case "starttls", "tls", "none":
	default:
		return "encryption must be one of starttls, tls, none"
	}
	if s.TimeoutSeconds < 1 || s.TimeoutSeconds > 600 {
		return "timeout_seconds must be 1..600"
	}
	return ""
}

// TestRequest is the body POST'd to /smtp/test/.
type TestRequest struct {
	Recipient string `json:"recipient"`
}

// Test handles POST /api/v1/admin/smtp/test/. The admin supplies a
// recipient; we read the live settings (NOT the request body — we
// could allow a candidate config but Rancher uses live settings to
// reduce the parameter surface, and the operator is one PUT away
// from doing the same thing) and send a templated test message.
func (h *SMTPHandler) Test(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	var req TestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	recipient := strings.TrimSpace(req.Recipient)
	if recipient == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "recipient is required")
		return
	}
	if _, err := mail.ParseAddress(recipient); err != nil {
		RespondError(w, http.StatusBadRequest, "validation_error", "recipient is not a valid email address")
		return
	}

	row, err := h.queries.GetSMTPSettings(r.Context(), email.SingletonSettingsID)
	if err != nil {
		RespondError(w, http.StatusServiceUnavailable, "not_configured", "SMTP settings are not configured yet")
		return
	}
	if !row.Enabled {
		RespondError(w, http.StatusBadRequest, "smtp_disabled", "SMTP is disabled; enable it before sending a test message")
		return
	}

	cfg := email.Settings{
		Enabled:       row.Enabled,
		Host:          row.Host,
		Port:          int(row.Port),
		Username:      row.Username,
		FromAddress:   row.FromAddress,
		FromName:      row.FromName,
		AuthMechanism: row.AuthMechanism,
		Encryption:    row.Encryption,
		RequireTLS:    row.RequireTls,
		Timeout:       time.Duration(row.TimeoutSeconds) * time.Second,
	}
	if row.PasswordEncrypted != "" && h.encryptor != nil {
		plain, err := h.encryptor.Decrypt(row.PasswordEncrypted)
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "decrypt_error", "Failed to decrypt stored SMTP password")
			return
		}
		cfg.Password = plain
	}

	sender := h.newTestSender(cfg)
	caller := callerUsername(r)
	if err := sender.Send(r.Context(), email.Message{
		To:       recipient,
		Template: email.TemplateTest,
		Data: map[string]any{
			"SentAt":      time.Now().UTC().Format(time.RFC3339),
			"TriggeredBy": caller,
		},
	}); err != nil {
		recordAudit(r, h.audit, "admin.smtp.test_failed", "smtp", row.ID.String(), recipient, map[string]any{
			"error": err.Error(),
		})
		RespondError(w, http.StatusBadGateway, "test_failed", err.Error())
		return
	}
	recordAudit(r, h.audit, "admin.smtp.test", "smtp", row.ID.String(), recipient, nil)
	RespondJSONUnwrapped(w, http.StatusOK, map[string]any{
		"success":   true,
		"recipient": recipient,
	})
}

// emailListItem is one row in the audit list response. Body fields
// are NOT included — the admin view only needs the metadata; the
// body could leak a reset link or a recovery-code-regen FYI.
type emailListItem struct {
	ID         string  `json:"id"`
	ToAddress  string  `json:"to_address"`
	Subject    string  `json:"subject"`
	Template   string  `json:"template"`
	Status     string  `json:"status"`
	Attempts   int     `json:"attempts"`
	LastError  string  `json:"last_error"`
	SentAt     *string `json:"sent_at"`
	CreatedAt  string  `json:"created_at"`
	UserID     *string `json:"user_id"`
}

// List handles GET /api/v1/admin/emails/. Paginated; default 50 per
// page, max 200.
func (h *SMTPHandler) List(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	if limit > 200 {
		limit = 200
	}

	rows, err := h.queries.ListEmailMessages(r.Context(), sqlc.ListEmailMessagesParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "read_error", "Failed to read email messages")
		return
	}
	total, _ := h.queries.CountEmailMessages(r.Context())

	items := make([]emailListItem, 0, len(rows))
	for _, row := range rows {
		var sentAt *string
		if row.SentAt.Valid {
			s := row.SentAt.Time.UTC().Format(time.RFC3339)
			sentAt = &s
		}
		var userID *string
		if row.UserID.Valid {
			s := uuid.UUID(row.UserID.Bytes).String()
			userID = &s
		}
		items = append(items, emailListItem{
			ID:        row.ID.String(),
			ToAddress: row.ToAddress,
			Subject:   row.Subject,
			Template:  row.Template,
			Status:    row.Status,
			Attempts:  int(row.Attempts),
			LastError: row.LastError,
			SentAt:    sentAt,
			CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
			UserID:    userID,
		})
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": total,
		"limit": limit,
		"offset": offset,
	})
}

// writeResponseFromRow renders the GET-shape payload from a freshly
// upserted row (so the dashboard can refresh from the PUT response
// without an extra round-trip).
func (h *SMTPHandler) writeResponseFromRow(w http.ResponseWriter, row sqlc.SmtpSettings) {
	RespondJSON(w, http.StatusOK, smtpSettingsResponse{
		Enabled:            row.Enabled,
		Host:               row.Host,
		Port:               int(row.Port),
		Username:           row.Username,
		Password:           PasswordSentinelEncrypted,
		PasswordConfigured: row.PasswordEncrypted != "",
		FromAddress:        row.FromAddress,
		FromName:           row.FromName,
		AuthMechanism:      row.AuthMechanism,
		Encryption:         row.Encryption,
		RequireTLS:         row.RequireTls,
		TimeoutSeconds:     int(row.TimeoutSeconds),
		UpdatedAt:          row.UpdatedAt.UTC().Format(time.RFC3339),
	})
}

func (h *SMTPHandler) requireSuperuser(r *http.Request) error {
	return requireSuperuserFromContext(r, h.queries)
}
