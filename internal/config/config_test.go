package config

import "testing"

func TestEnvironmentValidation(t *testing.T) {
	t.Setenv("CATALOG_REFRESH_MINUTES", "180")
	for _, value := range []string{"0", "-1", "3.1", "NaN", "Inf", "1e-300", "bad"} {
		t.Setenv("WFM_REQUESTS_PER_SECOND", value)
		if _, err := Load(); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	t.Setenv("WFM_REQUESTS_PER_SECOND", "2.5")
	for _, value := range []string{"0", "-1", "1.5", "999999999999999999"} {
		t.Setenv("CATALOG_REFRESH_MINUTES", value)
		if _, err := Load(); err == nil {
			t.Errorf("accepted interval %q", value)
		}
	}
	t.Setenv("CATALOG_REFRESH_MINUTES", "180")
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
}
