package codexreview

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativeReviewLeaseAllowsOnlyOneActiveOwner(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "lease")
	first, err := acquireNativeReviewLeaseAt(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	if _, err := acquireNativeReviewLeaseAt(directory); !errors.Is(err, ErrNativeReviewActive) {
		t.Fatalf("second lease error = %v, want %v", err, ErrNativeReviewActive)
	}
}

func TestNativeReviewLeaseReusesUnlockedMarker(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "lease")
	first, err := acquireNativeReviewLeaseAt(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := acquireNativeReviewLeaseAt(directory)
	if err != nil {
		t.Fatalf("reacquire unlocked marker: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeReviewLeaseReleasesWhenOwnerExits(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "lease")
	command := exec.Command(os.Args[0], "-test.run=^TestNativeReviewLeaseOwnerHelper$")
	command.Env = append(os.Environ(), "CODE_QUALITY_LEASE_HELPER="+directory)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("lease owner: %v\n%s", err, output)
	}

	lease, err := acquireNativeReviewLeaseAt(directory)
	if err != nil {
		t.Fatalf("acquire after owner exit: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeReviewLeaseOwnerHelper(t *testing.T) {
	directory := os.Getenv("CODE_QUALITY_LEASE_HELPER")
	if directory == "" {
		return
	}
	if _, err := acquireNativeReviewLeaseAt(directory); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}
