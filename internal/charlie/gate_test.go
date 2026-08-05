package charlie

import (
	"context"
	"errors"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type gateFeature bool

func (f gateFeature) BoolValue(context.Context, string, bool) bool { return bool(f) }

type gateConnection struct {
	row sqlc.CharlieConnection
	err error
}

func (g gateConnection) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return g.row, g.err
}

func TestActivationFailsClosedForEveryIncompleteState(t *testing.T) {
	cases := []struct {
		name        string
		features    featureReader
		connections activeConnectionReader
		want        ActivationState
		config      bool
	}{
		{"nil feature reader", nil, nil, ActivationFeatureDisabled, false},
		{"feature false", gateFeature(false), nil, ActivationFeatureDisabled, false},
		{"no connection store", gateFeature(true), nil, ActivationUnconfigured, false},
		{"connection read failed", gateFeature(true), gateConnection{err: errors.New("db")}, ActivationUnconfigured, false},
		{"install incomplete", gateFeature(true), gateConnection{row: sqlc.CharlieConnection{Active: true, OnboardingState: "secrets_written"}}, ActivationInstalling, false},
		{"emergency", gateFeature(true), gateConnection{row: sqlc.CharlieConnection{Active: true, OnboardingState: "active", EmergencyDisabled: true, RequestedMode: "auto", VerifiedMode: "auto"}}, ActivationEmergencyStop, false},
		{"mode disabled", gateFeature(true), gateConnection{row: sqlc.CharlieConnection{Active: true, OnboardingState: "active", RequestedMode: "disabled", VerifiedMode: "disabled"}}, ActivationInactive, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateActivation(context.Background(), tc.features, tc.connections)
			if got.Runnable || got.State != tc.want || got.Configurable != tc.config {
				t.Fatalf("activation=%+v, want non-runnable %s", got, tc.want)
			}
		})
	}
}

func TestActivationRequiresBothRequestedAndVerifiedPermissiveMode(t *testing.T) {
	for _, modes := range [][2]string{{"auto", "disabled"}, {"disabled", "auto"}, {"approval", "approval"}} {
		got := EvaluateActivation(context.Background(), gateFeature(true), gateConnection{row: sqlc.CharlieConnection{
			Active: true, OnboardingState: "active", RequestedMode: modes[0], VerifiedMode: modes[1],
		}})
		wantRunnable := modes[0] == "approval" && modes[1] == "approval"
		if got.Runnable != wantRunnable {
			t.Fatalf("requested=%s verified=%s runnable=%v", modes[0], modes[1], got.Runnable)
		}
	}
}
