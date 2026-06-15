package agentlifecycle

import "testing"

func TestStatusTerminalClassification(t *testing.T) {
	for _, status := range []string{StatusSucceeded, StatusFailed, StatusCancelled} {
		if !IsTerminal(status) {
			t.Fatalf("%q should be terminal", status)
		}
	}
	for _, status := range []string{StatusPending, StatusRunning} {
		if IsTerminal(status) {
			t.Fatalf("%q should not be terminal", status)
		}
	}
}
