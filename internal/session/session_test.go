package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fueav/code-quality/quality"
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

func TestMergeReviewsDeduplicatesRootsAndPreservesDistinctIDCollisions(t *testing.T) {
	first := quality.ModelReview{Findings: []quality.Finding{
		{ID: "F-001", RuleID: "DUP", CodeLocations: []quality.CodeLocation{{Path: "same.go", Line: 3}}, ProductionImpact: "older"},
		{ID: "F-002", RuleID: "FIRST", CodeLocations: []quality.CodeLocation{{Path: "first.go", Line: 4}}, ProductionImpact: "first"},
	}}
	second := quality.ModelReview{Findings: []quality.Finding{
		{ID: "F-009", RuleID: "DUP", CodeLocations: []quality.CodeLocation{{Path: "same.go", Line: 3}}, ProductionImpact: "newer"},
		{ID: "F-002", RuleID: "SECOND", CodeLocations: []quality.CodeLocation{{Path: "second.go", Line: 5}}, ProductionImpact: "second"},
	}}

	merged := mergeReviews(first, second)
	if len(merged.Findings) != 3 || merged.Findings[0].ProductionImpact != "newer" {
		t.Fatalf("merged findings = %#v", merged.Findings)
	}
	seenIDs := map[string]struct{}{}
	for _, finding := range merged.Findings {
		if _, exists := seenIDs[finding.ID]; exists {
			t.Fatalf("finding id remains duplicated: %#v", merged.Findings)
		}
		seenIDs[finding.ID] = struct{}{}
	}
}

func TestCleanupCloneCheckoutRejectsPathOutsideSession(t *testing.T) {
	sessionDir := t.TempDir()
	outside := t.TempDir()
	if err := cleanupCheckout("", sessionDir, outside, CheckoutModeClone); err == nil || !strings.Contains(err.Error(), "outside session") {
		t.Fatalf("cleanup error = %v", err)
	}
	if _, err := os.Lstat(outside); err != nil {
		t.Fatalf("outside path was removed: %v", err)
	}
}
