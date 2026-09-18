package reqctx

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestUserUUID(t *testing.T) {
	id := uuid.New()
	for _, tc := range []struct {
		name  string
		user  *User
		valid bool
	}{
		{"missing", nil, false},
		{"malformed", &User{ID: "not-a-uuid"}, false},
		{"nil UUID", &User{ID: uuid.Nil.String()}, false},
		{"authenticated", &User{ID: id.String()}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithUser(context.Background(), tc.user)
			got := UserUUID(ctx)
			if got.Valid != tc.valid || (tc.valid && got.Bytes != id) {
				t.Fatalf("UserUUID = %+v; want valid=%t id=%s", got, tc.valid, id)
			}
		})
	}
	if _, ok := AuthenticatedUser(context.Background()); ok {
		t.Fatal("empty context must not authenticate")
	}
}
