package handler

import "testing"

func TestToolStatusFromInstalledNormalizesHelmStatuses(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "helm pending install hyphen", in: "pending-install", want: "installing"},
		{name: "legacy pending install underscore", in: "pending_install", want: "installing"},
		{name: "helm pending upgrade hyphen", in: "pending-upgrade", want: "upgrading"},
		{name: "legacy pending uninstall underscore", in: "pending_uninstall", want: "uninstalling"},
		{name: "helm deployed", in: "deployed", want: "installed"},
		{name: "unknown passthrough", in: "superseded", want: "superseded"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolStatusFromInstalled(tt.in); got != tt.want {
				t.Fatalf("toolStatusFromInstalled(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
