package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRegularFileRejectsSymlinkAndEnforcesLimit(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	if err := os.WriteFile(target, []byte("trusted"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegularFile(link, 64); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("error = %v", err)
	}
	if _, err := ReadRegularFile(target, 3); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v", err)
	}
	raw, err := ReadRegularFile(target, 64)
	if err != nil || string(raw) != "trusted" {
		t.Fatalf("raw = %q, err = %v", raw, err)
	}
}
