package reviewrunner

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Fueav/code-quality/internal/nativereview"
	"github.com/Fueav/code-quality/internal/reviewplan"
	"github.com/Fueav/code-quality/quality"
)

func packageFixture(t *testing.T) PackageOptions {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(root, "review-current")
	if err := os.MkdirAll(filepath.Join(session, "output"), 0o700); err != nil {
		t.Fatal(err)
	}
	checkpoint, _ := json.Marshal(nativereview.SessionCheckpoint{SchemaVersion: 1, State: nativereview.StatePublished, Plan: reviewplan.Decision{ReviewIdentity: quality.ReviewIdentity{ReviewKey: "review-key"}}})
	for name, raw := range map[string][]byte{"checkpoint.json": checkpoint, "output/review-summary.md": []byte("PASS\n"), "output/review-summary.json": []byte(`{"result":"PASS"}`)} {
		if err := os.WriteFile(filepath.Join(session, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run, _ := json.Marshal(map[string]any{"evidence_dir": session, "summary_path": filepath.Join(session, "output", "review-summary.md"), "runner": map[string]string{"result_source": "EXECUTED", "lifecycle_status": "CURRENT"}})
	runPath := filepath.Join(t.TempDir(), "run.json")
	if err := os.WriteFile(runPath, run, 0o600); err != nil {
		t.Fatal(err)
	}
	return PackageOptions{RunPath: runPath, EvidenceRoot: root, Destination: filepath.Join(t.TempDir(), "artifacts")}
}

func TestPackageUsesExactSessionAndReusesVerifiedArchive(t *testing.T) {
	options := packageFixture(t)
	if err := os.WriteFile(filepath.Join(options.EvidenceRoot, "unrelated-secret"), []byte("not this invocation"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := PackageEvidence(options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PackageEvidence(options)
	if err != nil {
		t.Fatal(err)
	}
	if first.Reused || !second.Reused || first.ArchiveSHA256 != second.ArchiveSHA256 {
		t.Fatalf("invalid packaging reuse: %#v %#v", first, second)
	}
	file, err := os.Open(first.ArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zip, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer zip.Close()
	archive := tar.NewReader(zip)
	var names []string
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
	if !reflect.DeepEqual(names, []string{"checkpoint.json", "output/review-summary.json", "output/review-summary.md"}) {
		t.Fatalf("archive includes unrelated files: %v", names)
	}
}

func TestPackageRejectsCorruptCachedArchive(t *testing.T) {
	options := packageFixture(t)
	if _, err := PackageEvidence(options); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(options.EvidenceRoot, "archives", "*.tar.gz"))
	if err != nil || len(files) != 1 {
		t.Fatal(err, files)
	}
	if err := os.WriteFile(files[0], []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PackageEvidence(options); err == nil {
		t.Fatal("corrupt archive silently reused or regenerated")
	}
}

func TestPackageRejectsWrongSessionAndSymlinkEvidence(t *testing.T) {
	for _, kind := range []string{"root", "outside", "symlink", "summary"} {
		t.Run(kind, func(t *testing.T) {
			options := packageFixture(t)
			session := filepath.Join(options.EvidenceRoot, "review-current")
			if kind == "symlink" {
				if err := os.Symlink(options.RunPath, filepath.Join(session, "linked")); err != nil {
					t.Fatal(err)
				}
			} else {
				input := map[string]string{"evidence_dir": session}
				switch kind {
				case "root":
					input["evidence_dir"] = options.EvidenceRoot
				case "outside":
					input["evidence_dir"] = t.TempDir()
				case "summary":
					input["summary_path"] = options.RunPath
				}
				raw, _ := json.Marshal(input)
				if err := os.WriteFile(options.RunPath, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := PackageEvidence(options); err == nil {
				t.Fatalf("unsafe %s package succeeded", kind)
			}
		})
	}
}

func TestChangedSessionCannotReuseEarlierArchive(t *testing.T) {
	options := packageFixture(t)
	first, err := PackageEvidence(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options.EvidenceRoot, "review-current", "output", "review-summary.md"), []byte("ERROR\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := PackageEvidence(options)
	if err != nil {
		t.Fatal(err)
	}
	if second.Reused || first.ArchiveSHA256 == second.ArchiveSHA256 {
		t.Fatal("changed snapshot reused stale evidence")
	}
}

func TestInterruptedSessionRemovesPreviousPassFromDestination(t *testing.T) {
	first := packageFixture(t)
	if _, err := PackageEvidence(first); err != nil {
		t.Fatal(err)
	}
	second := packageFixture(t)
	second.Destination = first.Destination
	for _, name := range []string{"review-summary.md", "review-summary.json"} {
		if err := os.Remove(filepath.Join(second.EvidenceRoot, "review-current", "output", name)); err != nil {
			t.Fatal(err)
		}
	}
	result, err := PackageEvidence(second)
	if err != nil {
		t.Fatal(err)
	}
	if result.SummaryPath != "" {
		t.Fatal("interrupted session exposed a summary")
	}
	for _, name := range []string{"review-summary.md", "review-summary.json"} {
		if _, err := os.Lstat(filepath.Join(second.Destination, name)); !os.IsNotExist(err) {
			t.Fatalf("stale PASS retained: %s %v", name, err)
		}
	}
}
