package pricing

import "testing"

func TestContractBoundaries(t *testing.T) {
	if Round(2.5) != 2 || Round(3.5) != 4 {
		t.Fatal("bankers rounding contract violated")
	}
}
