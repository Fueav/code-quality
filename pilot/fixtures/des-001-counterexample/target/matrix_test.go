package matrix

import "testing"

func TestBoundedMatrixPreservesPermissions(t *testing.T) {
	tests := []struct {
		role, state string
		allowed     bool
	}{
		{role: "admin", state: "active", allowed: true},
		{role: "admin", state: "disabled", allowed: false},
		{role: "member", state: "active", allowed: true},
		{role: "member", state: "disabled", allowed: false},
		{role: "unknown", state: "active", allowed: false},
	}
	for _, test := range tests {
		if actual := IsPermitted(test.role, test.state); actual != test.allowed {
			t.Fatalf("IsPermitted(%q, %q) = %v, want %v", test.role, test.state, actual, test.allowed)
		}
	}
}
