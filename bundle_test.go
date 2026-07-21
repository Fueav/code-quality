package bundle

import (
	"strings"
	"testing"

	"github.com/Fueav/code-quality/quality"
)

func TestEmbeddedPolicyMatchesV11Contract(t *testing.T) {
	raw, err := PolicyManifest()
	if err != nil {
		t.Fatal(err)
	}
	policy, err := quality.DecodeStrict[quality.PolicyManifest](strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if errors := quality.ValidatePolicy(policy); len(errors) > 0 {
		t.Fatalf("embedded policy is invalid: %#v", errors)
	}
	if policy.PolicyVersion != "1.1.0" || len(policy.Rules) != 20 || policy.AgentLimit != 2 {
		t.Fatalf("embedded policy contract drifted: %#v", policy)
	}
}

func TestEmbeddedArtifactsAreAvailable(t *testing.T) {
	if rubric, err := Rubric(); err != nil || len(rubric) == 0 {
		t.Fatalf("embedded rubric unavailable: %v", err)
	} else if strings.Contains(string(rubric), "公司 AI 代码检查") {
		t.Fatal("embedded rubric still uses the retired product name")
	}
	for _, name := range []string{
		"review-request.schema.json",
		"model-review.schema.json",
		"review-result.schema.json",
		"verifier-review.schema.json",
	} {
		schema, err := Schema(name)
		if err != nil || len(schema) == 0 {
			t.Fatalf("embedded schema %s unavailable: %v", name, err)
		}
		if name == "model-review.schema.json" && strings.Contains(string(schema), `"$schema"`) {
			t.Fatal("Codex output schema uses an unsupported draft declaration")
		}
	}
}
