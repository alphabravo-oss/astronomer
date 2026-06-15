package envconfig

import "testing"

func TestNewViperReadsPrefixedEnvironment(t *testing.T) {
	t.Setenv("ASTRONOMER_TEST_VALUE", "from-env")
	v := NewViper("ASTRONOMER")
	SetDefaults(v, Default{Key: "test_value", Value: "default"})

	if got := v.GetString("test_value"); got != "from-env" {
		t.Fatalf("test_value = %q, want from-env", got)
	}
}

func TestBindEnvReadsUnsetDefaultKey(t *testing.T) {
	t.Setenv("CUSTOM_ONLY", "present")
	v := NewViper("")
	if err := BindEnv(v, "custom_only"); err != nil {
		t.Fatalf("BindEnv returned error: %v", err)
	}
	if got := v.GetString("custom_only"); got != "present" {
		t.Fatalf("custom_only = %q, want present", got)
	}
}
