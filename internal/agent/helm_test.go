package agent

import (
	"testing"
	"time"
)

func TestHelmReadyTimeout(t *testing.T) {
	t.Parallel()

	// Caller-provided seconds win.
	if got := helmReadyTimeout(30); got != 30*time.Second {
		t.Fatalf("helmReadyTimeout(30) = %v, want 30s", got)
	}

	// Zero/negative falls back to the helm CLI default so that install and
	// upgrade actually wait for workloads to become Ready (Wait=true) rather
	// than timing out immediately and reporting "deployed" prematurely.
	if got := helmReadyTimeout(0); got != defaultHelmReadyTimeout {
		t.Fatalf("helmReadyTimeout(0) = %v, want %v", got, defaultHelmReadyTimeout)
	}
	if got := helmReadyTimeout(-5); got != defaultHelmReadyTimeout {
		t.Fatalf("helmReadyTimeout(-5) = %v, want %v", got, defaultHelmReadyTimeout)
	}
}
