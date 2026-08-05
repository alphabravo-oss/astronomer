package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/google/uuid"
)

type CharlieOnboardingConsumer interface {
	Consume(context.Context, charlie.ValidatedOnboarding, uuid.UUID) (charlie.OnboardingStatus, error)
}

type CharlieOnboardingHandler struct {
	consumer CharlieOnboardingConsumer
	now      func() time.Time
	log      *slog.Logger
}

func NewCharlieOnboardingHandler(consumer CharlieOnboardingConsumer) *CharlieOnboardingHandler {
	return &CharlieOnboardingHandler{consumer: consumer, now: time.Now, log: slog.Default()}
}

// openapi:request CharlieOnboardingRequest
type charlieOnboardingRequest struct {
	Package                     json.RawMessage `json:"package"`
	SigningPublicKey            string          `json:"signing_public_key"`
	ConfirmedSigningKeyID       string          `json:"confirmed_signing_key_id"`
	ConfirmedSigningFingerprint string          `json:"confirmed_signing_fingerprint"`
	ExpectedDeploymentID        string          `json:"expected_deployment_id"`
	ExpectedRouteID             string          `json:"expected_route_id"`
}

func (h *CharlieOnboardingHandler) Validate(w http.ResponseWriter, r *http.Request) {
	validated, ok := h.validateRequest(w, r)
	if !ok {
		return
	}
	RespondJSON(w, http.StatusOK, validated.SafeStatus("validated", false))
}

func (h *CharlieOnboardingHandler) Import(w http.ResponseWriter, r *http.Request) {
	validated, ok := h.validateRequest(w, r)
	if !ok {
		return
	}
	user, authenticated := appmiddleware.GetAuthenticatedUser(r.Context())
	if !authenticated {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication is required")
		return
	}
	actorID, err := uuid.Parse(user.ID)
	if err != nil || h.consumer == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Charlie onboarding is unavailable")
		return
	}
	status, err := h.consumer.Consume(r.Context(), validated, actorID)
	if err != nil {
		h.log.Warn("Charlie onboarding consume failed",
			slog.String("failure_code", charlie.OnboardingFailureCode(err)),
			slog.String("correlation_id", appmiddleware.GetCorrelationID(r.Context())),
		)
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Charlie onboarding could not be consumed safely")
		return
	}
	RespondJSON(w, http.StatusOK, status)
}

func (h *CharlieOnboardingHandler) validateRequest(w http.ResponseWriter, r *http.Request) (charlie.ValidatedOnboarding, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		RespondRequestError(w, r, http.StatusUnsupportedMediaType, apierror.InvalidRequest, "Content-Type must be application/json")
		return charlie.ValidatedOnboarding{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, charlie.MaxOnboardingPackageBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request charlieOnboardingRequest
	if err := decoder.Decode(&request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			RespondRequestError(w, r, http.StatusRequestEntityTooLarge, apierror.InvalidBody, "Onboarding request exceeds the maximum size")
			return charlie.ValidatedOnboarding{}, false
		}
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid onboarding request")
		return charlie.ValidatedOnboarding{}, false
	}
	if err := ensureJSONEOF(decoder); err != nil || len(request.Package) == 0 ||
		strings.TrimSpace(request.SigningPublicKey) == "" || strings.TrimSpace(request.ConfirmedSigningFingerprint) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid onboarding request")
		return charlie.ValidatedOnboarding{}, false
	}
	now := time.Now().UTC()
	if h != nil && h.now != nil {
		now = h.now().UTC()
	}
	validated, err := charlie.ValidateOnboardingPackage(request.Package, charlie.OnboardingConfirmation{
		SigningPublicKeyBase64: request.SigningPublicKey, ConfirmedSigningKeyID: request.ConfirmedSigningKeyID,
		ConfirmedSigningFingerprint: request.ConfirmedSigningFingerprint,
		ExpectedDeploymentID:        request.ExpectedDeploymentID, ExpectedRouteID: request.ExpectedRouteID, Now: now,
		ExpectedMCPURL: "https://astronomer-charlie-mcp.astronomer.svc:7444/mcp",
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Onboarding package verification failed")
		return charlie.ValidatedOnboarding{}, false
	}
	return validated, true
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return err
	}
	return nil
}
