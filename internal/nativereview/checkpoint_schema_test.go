package nativereview

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	bundle "github.com/Fueav/code-quality"
	"github.com/Fueav/code-quality/internal/reviewplan"
	"github.com/Fueav/code-quality/quality"
)

// Build and persist the actual runtime checkpoint without invoking a Provider.
// Release version changes must keep the published header constraints in sync.
func TestCheckpointSchemaMatchesPersistedRuntimeVersion(t *testing.T) {
	session, request := nativeFixture(t)
	contract, err := ResolveContract(NewCodexProvider(""), "", "", "", quality.ReviewScopeFull)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := reviewplan.Build(context.Background(), reviewplan.Input{
		RepositoryPath: session.RepositoryDirectory(), Base: request.BaseCommit, Target: request.TargetCommit,
		DiffReason: "checkpoint-schema-test", ReviewScope: quality.ReviewScopeFull,
		Contract: contract.Contract, ParentContract: contract.Contract, Environment: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := newSessionCheckpoint(session, plan, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCheckpoint(session.Directory(), &checkpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAndVerifyCheckpoint(session.Directory()); err != nil {
		t.Fatalf("runtime rejected its own current checkpoint: %v", err)
	}
	raw, err := bundle.Schema("native-session-checkpoint.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Const json.RawMessage `json:"const"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(session.Directory(), "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]json.RawMessage
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"schema_version", "tool_version"} {
		constraint := schema.Properties[field].Const
		if len(constraint) == 0 || !bytes.Equal(bytes.TrimSpace(constraint), bytes.TrimSpace(persisted[field])) {
			t.Errorf("schema %s const %s rejects runtime value %s", field, constraint, persisted[field])
		}
	}
	if checkpoint.SchemaVersion != 1 || checkpoint.ToolVersion != quality.SkillVersion {
		t.Fatalf("unexpected runtime checkpoint version: %d / %q", checkpoint.SchemaVersion, checkpoint.ToolVersion)
	}
	// Updating the advertised current version must not relax cross-version resume.
	for _, version := range []string{"0.5.9", "999.0.0"} {
		checkpoint.ToolVersion = version
		if err := writeCheckpoint(session.Directory(), &checkpoint); err != nil {
			t.Fatal(err)
		}
		if _, err := loadAndVerifyCheckpoint(session.Directory()); err == nil {
			t.Errorf("runtime accepted checkpoint from tool version %s", version)
		}
	}
}
