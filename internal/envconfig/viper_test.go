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

func TestLookupReadsExplicitEnvironmentKey(t *testing.T) {
	t.Setenv("TEST_EXPLICIT_SECRET", "secret-value")
	if got, ok := Lookup("TEST_EXPLICIT_SECRET"); !ok || got != "secret-value" {
		t.Fatalf("Lookup = %q, %v; want secret-value, true", got, ok)
	}
	if _, ok := Lookup("TEST_EXPLICIT_SECRET_MISSING"); ok {
		t.Fatal("Lookup reported an unset key as present")
	}
}
