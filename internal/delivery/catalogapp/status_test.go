package catalogapp

import "testing"

func TestProjectStatusPrefersDeletionLifecycle(t *testing.T) {
	for _, test := range []struct {
		name, deletion, deployment, want string
	}{
		{name: "active without observation", deletion: "active", want: "pending"},
		{name: "active deployment", deletion: "active", deployment: "ready", want: "ready"},
		{name: "deleting overrides stale install", deletion: "deleting", deployment: "pending", want: "deleting"},
		{name: "deleted overrides stale failure", deletion: "deleted", deployment: "failed", want: "removed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := projectStatus(test.deletion, test.deployment, "observed-error"); got.Phase != test.want || got.LastErrorCode != "observed-error" {
				t.Fatalf("status = %+v, want phase %q with preserved error", got, test.want)
			}
		})
	}
}

func TestOnlyUpgradeAdvancesInstallationRevision(t *testing.T) {
	for _, test := range []struct {
		status string
		want   bool
	}{
		{status: "installing", want: false},
		{status: "upgrading", want: true},
		{status: "rolling_back", want: false},
	} {
		if got := advancesInstallationRevision(test.status); got != test.want {
			t.Fatalf("advancesInstallationRevision(%q) = %t, want %t", test.status, got, test.want)
		}
	}
}
