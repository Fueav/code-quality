package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverEvidenceAcceptsCommitBoundDigestVerifiedSource(t *testing.T) {
	repo := t.TempDir()
	target := strings.Repeat("b", 40)
	writeEvidenceFixture(t, repo, "change", target, false)
	destination := filepath.Join(t.TempDir(), "evidence")
	context, err := DiscoverEvidence(repo, target, destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(context.Sources) != 1 || len(context.Rejected) != 0 || context.Sources[0].Mode != "change" {
		t.Fatalf("context = %#v", context)
	}
	for _, name := range []string{"summary.json", "artifact_manifest.json", "gates.json"} {
		info, err := os.Lstat(filepath.Join(destination, "change", name))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("copied evidence %s is invalid: %v", name, err)
		}
	}
}

func TestDiscoverEvidenceRejectsStaleOrCorruptSourcesWithoutFailing(t *testing.T) {
	target := strings.Repeat("b", 40)
	for name, configure := range map[string]func(string){
		"stale":   func(repo string) { writeEvidenceFixture(t, repo, "change", strings.Repeat("a", 40), false) },
		"corrupt": func(repo string) { writeEvidenceFixture(t, repo, "change", target, true) },
	} {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			configure(repo)
			context, err := DiscoverEvidence(repo, target, filepath.Join(t.TempDir(), "evidence"))
			if err != nil {
				t.Fatal(err)
			}
			if len(context.Sources) != 0 || len(context.Rejected) != 1 || context.Rejected[0].Mode != "change" {
				t.Fatalf("context = %#v", context)
			}
		})
	}
}

func TestDiscoverEvidenceOrdinaryRepositoryHasNoDependency(t *testing.T) {
	context, err := DiscoverEvidence(t.TempDir(), strings.Repeat("b", 40), filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	if context.Sources == nil || context.Rejected == nil || len(context.Sources) != 0 || len(context.Rejected) != 0 {
		t.Fatalf("context = %#v", context)
	}
}

func TestDiscoverEvidenceRejectsSymlinkedArtifactsAncestor(t *testing.T) {
	repo := t.TempDir()
	external := t.TempDir()
	target := strings.Repeat("b", 40)
	writeEvidenceFixture(t, external, "change", target, false)
	if err := os.Symlink(filepath.Join(external, ".artifacts"), filepath.Join(repo, ".artifacts")); err != nil {
		t.Fatal(err)
	}
	context, err := DiscoverEvidence(repo, target, filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	if len(context.Sources) != 0 || len(context.Rejected) != 3 {
		t.Fatalf("context = %#v", context)
	}
	for _, rejected := range context.Rejected {
		if !strings.Contains(rejected.Reason, "non-symlink directory") {
			t.Fatalf("rejection = %#v", rejected)
		}
	}
}

func TestCleanEvidencePathRejectsPortableTraversal(t *testing.T) {
	for _, path := range []string{"../secret", `..\secret`, `/absolute`, `nested\windows`} {
		if cleanEvidencePath(path) {
			t.Fatalf("path %q was accepted", path)
		}
	}
	for _, path := range []string{"gates.json", "nested/gates.json"} {
		if !cleanEvidencePath(path) {
			t.Fatalf("path %q was rejected", path)
		}
	}
}

func writeEvidenceFixture(t *testing.T, repo, mode, target string, corrupt bool) {
	t.Helper()
	dir := filepath.Join(repo, ".artifacts", mode)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	artifact := []byte(`{"status":"passed"}`)
	if err := os.WriteFile(filepath.Join(dir, "gates.json"), artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	artifactDigest := sha256.Sum256(artifact)
	manifest := map[string]any{
		"schema_version": 1,
		"artifacts": []map[string]any{{
			"path": "gates.json", "sha256": hex.EncodeToString(artifactDigest[:]), "size_bytes": len(artifact),
		}},
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "artifact_manifest.json"), manifestRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestDigest := sha256.Sum256(manifestRaw)
	digest := hex.EncodeToString(manifestDigest[:])
	if corrupt {
		digest = strings.Repeat("0", 64)
	}
	summary := map[string]any{
		"schema_version": 1, "mode": mode, "overall": "passed",
		"artifact_manifest_sha256": digest,
		"artifacts":                manifest["artifacts"],
		"git":                      map[string]any{"head_sha": target},
		"gates":                    []any{},
	}
	summaryRaw, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), summaryRaw, 0o600); err != nil {
		t.Fatal(err)
	}
}
