package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeProjectBindingLister struct {
	rows  []sqlc.ProjectRoleBinding
	err   error
	calls int
}

func (f *fakeProjectBindingLister) ListProjectRoleBindings(_ context.Context, arg sqlc.ListProjectRoleBindingsParams) ([]sqlc.ProjectRoleBinding, error) {
	f.calls++
	if arg.Limit != 1 {
		return nil, errors.New("existence check must not page the whole table")
	}
	return f.rows, f.err
}

func captureWarnings(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})), &buf
}

// TestWarnInertProjectBindings covers step 4 of project-bindings-inert-by-default:
// an install that turned the filter off keeps its project bindings, and loses
// the cluster-wide list/watch filtering they depend on. The warning is the only
// signal, so it must fire exactly when the filter is off and bindings exist —
// and it must describe what the flag ACTUALLY gates (the expansion itself is
// unconditional), which the wantText assertions below pin.
func TestWarnInertProjectBindings(t *testing.T) {
	oneRow := []sqlc.ProjectRoleBinding{{}}

	for _, tc := range []struct {
		name       string
		rows       []sqlc.ProjectRoleBinding
		err        error
		nsScoped   bool
		wantWarn   bool
		wantCalled bool
	}{
		{"bindings exist and filter is off", oneRow, nil, false, true, true},
		{"bindings exist and filter is on", oneRow, nil, true, false, false},
		{"no bindings", nil, nil, false, false, true},
		{"query fails", nil, errors.New("boom"), false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger, buf := captureWarnings(t)
			fake := &fakeProjectBindingLister{rows: tc.rows, err: tc.err}

			warnInertProjectBindings(context.Background(), fake, tc.nsScoped, logger)

			warned := strings.Contains(buf.String(), "namespace-scoped RBAC is disabled")
			if warned != tc.wantWarn {
				t.Fatalf("warned = %v, want %v (log: %q)", warned, tc.wantWarn, buf.String())
			}
			if warned {
				// The old text claimed project grants "do not authorize cluster
				// resources", which stopped being true when the expansion went
				// unconditional. Pin the corrected semantics so a revert to the
				// misleading wording fails here.
				for _, want := range []string{
					"still reach namespace-explicit paths",
					"403'd off cluster-wide list/watch routes",
				} {
					if !strings.Contains(buf.String(), want) {
						t.Fatalf("warning missing %q; log: %q", want, buf.String())
					}
				}
				if strings.Contains(buf.String(), "do not authorize cluster resources") {
					t.Fatalf("warning still claims project grants are inert; log: %q", buf.String())
				}
			}
			if called := fake.calls > 0; called != tc.wantCalled {
				t.Fatalf("queried = %v, want %v", called, tc.wantCalled)
			}
		})
	}
}

// A nil querier or logger must not panic the server's startup path.
func TestWarnInertProjectBindingsNilSafe(t *testing.T) {
	warnInertProjectBindings(context.Background(), nil, false, slog.Default())
	warnInertProjectBindings(context.Background(), &fakeProjectBindingLister{}, false, nil)
}
