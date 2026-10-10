package upgrade

import "testing"

func TestLegacyUpgradeArgumentsPreserveShellModeAndHelpOrder(t *testing.T) {
	for _, row := range []struct {
		arguments []string
		mode      Mode
		target    string
		help      bool
	}{
		{[]string{"1.2.2"}, Preview, "v1.2.2", false},
		{[]string{"--yes", "v1.2.2"}, Execute, "v1.2.2", false},
		{[]string{"v1.2.2", "--yes", "--yes"}, Execute, "v1.2.2", false},
		{[]string{"--yes", "--dry-run-only", "v1.2.2"}, Preview, "v1.2.2", false},
		{[]string{"--help"}, Preview, "", true},
		{[]string{"v1.2.2", "--help", "invalid-second-version"}, Preview, "v1.2.2", true},
	} {
		got, err := ParseLegacyArguments(row.arguments)
		if err != nil || got.Mode != row.mode || got.Target != row.target || got.Help != row.help {
			t.Fatalf("legacy argument contract differs: %+v %v", got, err)
		}
	}
}
func TestLegacyUpgradeArgumentsRetainUsageExitForUnsupportedInputs(t *testing.T) {
	for _, arguments := range [][]string{nil, {"--unknown", "--help"}, {"--"}, {"v1.2.2", "v1.2.3"}, {"v2.0.0"}, {"v1.2.2-rc.1"}, {"latest"}} {
		_, err := ParseLegacyArguments(arguments)
		if err == nil || ParseUsageExit(err) != 2 {
			t.Fatalf("malformed legacy argument admitted: %v", arguments)
		}
	}
}
