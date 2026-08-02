package codexreview

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
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

func TestNativeReviewLeaseRemainsHeldByInheritedFile(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "lease")
	lease, err := acquireNativeReviewLeaseAt(directory)
	if err != nil {
		t.Fatal(err)
	}

	command := exec.Command(os.Args[0], "-test.run=^TestNativeReviewLeaseInheritedFileHelper$")
	command.Env = append(os.Environ(), "CODE_QUALITY_INHERITED_LEASE_HELPER=1")
	command.ExtraFiles = []*os.File{lease.InheritedFile()}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "lease inherited" {
		t.Fatalf("child stdout = %q, stderr = %q, error = %v", scanner.Text(), stderr.String(), scanner.Err())
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireNativeReviewLeaseAt(directory); !errors.Is(err, ErrNativeReviewActive) {
		t.Fatalf("lease after wrapper close = %v, want %v", err, ErrNativeReviewActive)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("inherited lease child: %v, stderr = %q", err, stderr.String())
	}

	afterChild, err := acquireNativeReviewLeaseAt(directory)
	if err != nil {
		t.Fatalf("lease after child exit: %v", err)
	}
	if err := afterChild.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeReviewLeaseUsesOwnedUserCache(t *testing.T) {
	cacheDirectory, err := nativeReviewCacheDirectory()
	if err != nil {
		t.Fatal(err)
	}
	leaseDirectory, err := nativeReviewLeaseDirectory()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cacheDirectory, leaseDirectory)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) {
		t.Fatalf("lease directory %q is not below user cache %q: relative=%q, error=%v", leaseDirectory, cacheDirectory, relative, err)
	}
	cacheInfo, err := os.Stat(cacheDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if !ownedByCurrentUser(cacheInfo) || cacheInfo.Mode().Perm()&0o022 != 0 {
		t.Fatalf("user cache is not owner-controlled by uid %d: mode=%v", os.Getuid(), cacheInfo.Mode())
	}
}

func TestNativeReviewLeaseNamespaceIgnoresCacheEnvironment(t *testing.T) {
	before, err := nativeReviewLeaseDirectory()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	after, err := nativeReviewLeaseDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("lease directory changed with cache environment: before=%q after=%q", before, after)
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

func TestNativeReviewLeaseInheritedFileHelper(t *testing.T) {
	if os.Getenv("CODE_QUALITY_INHERITED_LEASE_HELPER") == "" {
		return
	}
	file := os.NewFile(3, "native-review-lease")
	if file == nil {
		t.Fatal("inherited lease file is unavailable")
	}
	if _, err := file.Stat(); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, "lease inherited")
	time.Sleep(500 * time.Millisecond)
	os.Exit(0)
}
