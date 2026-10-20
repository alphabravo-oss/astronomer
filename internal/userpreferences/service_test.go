package userpreferences

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
)

func TestServiceMissingCapabilitiesRemainExplicit(t *testing.T) {
	for _, service := range []*Service{nil, New(nil, nil)} {
		if service.CanRead() || service.CanReplace() {
			t.Fatal("missing adapter gained a capability")
		}
		if _, err := service.Get(context.Background(), uuid.New()); !errors.Is(err, ErrNotConfigured) {
			t.Fatal("missing reader did not fail closed")
		}
		command, err := Prepare(Defaults())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Replace(context.Background(), uuid.New(), command, Replay{}, audit.Intent{}); !errors.Is(err, ErrNotConfigured) {
			t.Fatal("missing writer did not fail closed")
		}
	}
}
func TestPrepareOwnsValidatedDocumentWithoutBorrowedSlices(t *testing.T) {
	input := Defaults()
	input.Favorites = []string{"/dashboard"}
	command, err := Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	original := command.Digest()
	input.Favorites[0] = "/changed"
	returned := command.Document()
	returned.Favorites[0] = "/also-changed"
	if command.Document().Favorites[0] != "/dashboard" {
		t.Fatal("command borrowed caller slice")
	}
	digest, err := DocumentDigest(command.Document())
	if err != nil || digest != original {
		t.Fatal("prepared command identity changed")
	}
}
func TestServiceRejectsEmptyCommandBeforeTransaction(t *testing.T) {
	begins := 0
	service := New(nil, func(_ context.Context, _ func(MutationTx) error) error { begins++; return nil })
	if _, err := service.Replace(context.Background(), uuid.New(), Command{}, Replay{}, audit.Intent{}); !errors.Is(err, ErrReceiptInvalid) || begins != 0 {
		t.Fatal("unvalidated command reached transaction")
	}
	command, err := Prepare(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Replace(context.Background(), uuid.New(), command, Replay{Scope: "partial"}, audit.Intent{}); !errors.Is(err, ErrReceiptInvalid) || begins != 0 {
		t.Fatal("partial replay identity reached transaction")
	}
}
