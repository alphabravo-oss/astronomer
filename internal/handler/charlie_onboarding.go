package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/pkg/logging"
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
	Endpoint                    string          `json:"endpoint"`
	ConnectToken                string          `json:"connect_token"`
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
	user, authenticated := reqctx.AuthenticatedUser(r.Context())
	if !authenticated {
		apiutil.WriteError(w, r, http.StatusUnauthorized, apiutil.AuthenticationRequired, "Authentication is required")
		return
	}
	actorID, err := uuid.Parse(user.ID)
	if err != nil || h.consumer == nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.InternalError, "Charlie onboarding is unavailable")
		return
	}
	status, err := h.consumer.Consume(r.Context(), validated, actorID)
	if err != nil {
		charlie.LogOperationalFailure(r.Context(), *logging.Default(), charlie.OnboardingFailureCode(err), reqctx.CorrelationID(r.Context()))
		apiutil.WriteError(w, r, http.StatusConflict, apiutil.Conflict, "Charlie onboarding could not be consumed safely")
		return
	}
	RespondJSON(w, http.StatusOK, status)
}

func (h *CharlieOnboardingHandler) validateRequest(w http.ResponseWriter, r *http.Request) (charlie.ValidatedOnboarding, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		apiutil.WriteError(w, r, http.StatusUnsupportedMediaType, apiutil.InvalidRequest, "Content-Type must be application/json")
		return charlie.ValidatedOnboarding{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, charlie.MaxOnboardingPackageBytes*2)
	var request charlieOnboardingRequest
	if err := decodeStrictJSONBody(r, &request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			apiutil.WriteError(w, r, http.StatusRequestEntityTooLarge, apiutil.InvalidBody, "Onboarding request exceeds the maximum size")
			return charlie.ValidatedOnboarding{}, false
		}
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidBody, "Invalid onboarding request")
		return charlie.ValidatedOnboarding{}, false
	}
	now := time.Now().UTC()
	if h != nil && h.now != nil {
		now = h.now().UTC()
	}
	pkg := request.Package
	confirmation := charlie.OnboardingConfirmation{
		SigningPublicKeyBase64: request.SigningPublicKey, ConfirmedSigningKeyID: request.ConfirmedSigningKeyID,
		ConfirmedSigningFingerprint: request.ConfirmedSigningFingerprint,
		ExpectedDeploymentID:        request.ExpectedDeploymentID, ExpectedRouteID: request.ExpectedRouteID, Now: now,
		ExpectedMCPURL: "https://astronomer-charlie-mcp.astronomer.svc:7444/mcp",
	}
	if strings.TrimSpace(request.ConnectToken) != "" || strings.TrimSpace(request.Endpoint) != "" {
		decoded, parsed, err := charlie.ParseConnectToken(request.Endpoint, request.ConnectToken)
		if err != nil {
			apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Charlie connect token verification failed")
			return charlie.ValidatedOnboarding{}, false
		}
		pkg = decoded
		parsed.Now = now
		parsed.ExpectedMCPURL = confirmation.ExpectedMCPURL
		confirmation = parsed
	} else if len(request.Package) == 0 || strings.TrimSpace(request.SigningPublicKey) == "" || strings.TrimSpace(request.ConfirmedSigningFingerprint) == "" {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidBody, "Invalid onboarding request")
		return charlie.ValidatedOnboarding{}, false
	}
	validated, err := charlie.ValidateOnboardingPackage(pkg, confirmation)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Onboarding package verification failed")
		return charlie.ValidatedOnboarding{}, false
	}
	return validated, true
}
