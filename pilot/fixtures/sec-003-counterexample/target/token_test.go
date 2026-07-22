package credential

import (
	"strings"
	"testing"
)

const testOnlyToken = "TEST_ONLY_NOT_ACCEPTED_BY_PROVIDER"

func providerAccepts(token string) bool {
	return strings.HasPrefix(token, "prod_live_")
}

func TestFixtureTokenIsNotRoutable(t *testing.T) {
	if providerAccepts(testOnlyToken) {
		t.Fatal("test-only token unexpectedly matches provider credentials")
	}
}
