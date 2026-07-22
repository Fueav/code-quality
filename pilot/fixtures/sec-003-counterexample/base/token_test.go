package credential

import "testing"

func fixtureToken() string { return "generated-test-token" }

func TestFixtureToken(t *testing.T) {
	if fixtureToken() == "" {
		t.Fatal("missing test fixture")
	}
}
