package codexreview

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeReviewLeaseAllowsOnlyOneActiveOwner(t *testing.T) {
	directory := t.TempDir()
	first, err := acquireNativeReviewLeaseAt(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	if _, err := acquireNativeReviewLeaseAt(directory); !errors.Is(err, ErrNativeReviewActive) {
		t.Fatalf("second lease error = %v, want %v", err, ErrNativeReviewActive)
	}
}

func TestNativeReviewLeaseReacquiresAfterUnlock(t *testing.T) {
	directory := t.TempDir()
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
	directory := t.TempDir()
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
	directory := t.TempDir()
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

func TestNativeReviewLeaseNeedsNoWritablePath(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(directory, 0o700)

	lease, err := acquireNativeReviewLeaseAt(directory)
	if err != nil {
		t.Fatalf("acquire on read-only directory: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("read-only lease created filesystem state: %v", entries)
	}
	if info, err := os.Stat(directory); err != nil || info.Mode().Perm() != 0o500 {
		t.Fatalf("lease changed directory metadata: info=%v error=%v", info, err)
	}
}

func TestNativeReviewLeaseUsesOwnedAccountHome(t *testing.T) {
	homeDirectory, err := currentAccountHomeDirectory()
	if err != nil {
		t.Fatal(err)
	}
	leaseDirectory, err := nativeReviewLeaseDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if leaseDirectory != homeDirectory {
		t.Fatalf("lease target = %q, want account home %q", leaseDirectory, homeDirectory)
	}
	info, err := os.Stat(leaseDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if !ownedByCurrentUser(info) || info.Mode().Perm()&0o022 != 0 {
		t.Fatalf("account home is not owner-controlled by uid %d: mode=%v", os.Getuid(), info.Mode())
	}
}

func TestNativeReviewLeaseNamespaceIgnoresCacheEnvironmentAcrossProcesses(t *testing.T) {
	probe := func(home string) string {
		t.Helper()
		command := exec.Command(os.Args[0], "-test.run=^TestNativeReviewLeaseNamespaceProbeHelper$")
		command.Env = []string{
			"CODE_QUALITY_LEASE_NAMESPACE_PROBE=1",
			"HOME=" + home,
			"XDG_CACHE_HOME=" + filepath.Join(home, "cache"),
			"USER=lease-probe",
		}
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("lease namespace probe: %v\n%s", err, output)
		}
		return strings.TrimSpace(string(output))
	}

	before := probe(filepath.Join(t.TempDir(), "home-a"))
	after := probe(filepath.Join(t.TempDir(), "home-b"))
	if before == "" || after != before {
		t.Fatalf("lease directory changed with cache environment: before=%q after=%q", before, after)
	}
}

func TestLookupPasswdHomeFailsClosed(t *testing.T) {
	passwd := strings.NewReader("root:x:0:0:root:/root:/bin/sh\nreview:x:501:501::/home/review:/bin/sh\n")
	home, err := lookupPasswdHome(passwd, 501)
	if err != nil || home != "/home/review" {
		t.Fatalf("lookup passwd home = %q, %v", home, err)
	}
	if _, err := lookupPasswdHome(strings.NewReader("root:x:0:0:root:/root:/bin/sh\n"), 501); err == nil {
		t.Fatal("missing passwd UID fell back instead of failing")
	}
}

func TestNativeReviewLeaseNamespaceProbeHelper(t *testing.T) {
	if os.Getenv("CODE_QUALITY_LEASE_NAMESPACE_PROBE") == "" {
		return
	}
	directory, err := nativeReviewLeaseDirectory()
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, directory)
	os.Exit(0)
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
