package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStreamRequiresAccessJWT(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"access", "refresh", PurposeTOTPChallenge, PurposeTOTPEnrollOnly} {
		for _, stream := range []string{StreamKindExec, StreamKindShell, StreamKindLogs, StreamKindEvents} {
			t.Run(kind+"/"+stream, func(t *testing.T) {
				t.Parallel()
				manager := MustNewJWTManager("test-stream-boundary", 60)
				userID := uuid.New()
				var token string
				var err error
				switch kind {
				case "access":
					token, err = manager.GenerateAccessToken(userID)
				case "refresh":
					token, err = manager.GenerateRefreshToken(userID)
				default:
					token, err = manager.GeneratePurposeToken(userID, kind, time.Minute)
				}
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodGet, "/stream", nil)
				request.Header.Set("Authorization", "Bearer "+token)
				got, ok := AuthorizeStreamRequestWithTickets(request, nil, manager, nil, stream, uuid.New())
				if kind == "access" {
					if !ok || got != userID {
						t.Fatal("valid access JWT rejected")
					}
				} else if ok || got != uuid.Nil {
					t.Fatal("non-access JWT accepted as a stream credential")
				}
			})
		}
	}
}

func TestStreamValidationPreservesRequestCancellation(t *testing.T) {
	t.Parallel()
	manager := MustNewJWTManager("test-stream-context", 60)
	token, err := manager.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	manager.SetRevocationChecker(newFakeRevocationChecker())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/stream", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if _, ok := AuthorizeStreamRequest(request, nil, manager); ok {
		t.Fatal("canceled revocation lookup authenticated the stream")
	}
}
