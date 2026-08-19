package bundle

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Fueav/code-quality/quality"
)

func TestNativeResultV3SchemaRemainsFrozen(t *testing.T) {
	raw, err := Schema("review-result-v3.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != "ecfe2d98d4d06c297119cade7ac56f4965d84aa7598fcce0c14b5f42291fea1a" {
		t.Fatalf("v3 native result schema changed without a versioned contract: %s", got)
	}
}

func TestNativeResultV4SchemaIsProviderAware(t *testing.T) {
	raw, err := Schema("review-result-v4.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{`"schema_version": {"const": 4}`, `"codex"`, `"claude-code"`, `"provider_invocations": {"const": 1}`} {
		if !strings.Contains(text, required) {
			t.Errorf("v4 schema is missing %s", required)
		}
	}
	if strings.Contains(text, `"model_calls"`) {
		t.Fatal("v4 schema still claims one underlying model call")
	}
}

func TestNativeResultV5SchemaCarriesPullRequestIdentity(t *testing.T) {
	raw, err := Schema("review-result-v5.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{`"schema_version": {"const": 5}`, `"pull_request"`, `"base_tip_commit"`, `"run_url"`, `"execution_profile"`, `"production-ci"`} {
		if !strings.Contains(text, required) {
			t.Errorf("v5 schema is missing %s", required)
		}
	}
}

func TestNativeResultV6SchemaIsAReleaseGate(t *testing.T) {
	raw, err := Schema("review-result-v6.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{`"schema_version": {"const": 6}`, `"PASS"`, `"BLOCK"`, `"ERROR"`, `"release_gate"`, `"continue_release"`, `"hold_release"`, `"reason"`, `"suggestion"`} {
		if !strings.Contains(text, required) {
			t.Errorf("v6 schema is missing %s", required)
		}
	}
}

func TestNativeResultV7SchemaIsPriorityAware(t *testing.T) {
	raw, err := Schema("review-result-v7.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{`"schema_version": {"const": 7}`, `"PASS"`, `"BLOCK"`, `"priority": {"enum": [0, 1]}`, `"priority": {"enum": [2, 3]}`} {
		if !strings.Contains(text, required) {
			t.Errorf("v7 schema is missing %s", required)
		}
	}
}

func TestNativeResultV8SchemaCarriesScopeIdentityAndLineage(t *testing.T) {
	raw, err := Schema("review-result-v8.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{`"schema_version": {"const": 8}`, `"review_key"`, `"contract_digest"`, `"review_scope"`, `"INCREMENTAL"`, `"previous_finding_resolutions"`, `"new_findings"`} {
		if !strings.Contains(text, required) {
			t.Errorf("v8 schema is missing %s", required)
		}
	}
}

func TestNativeResultV9SchemaCarriesRestrictedAdjudication(t *testing.T) {
	raw, err := Schema("review-result-v9.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{`"schema_version": {"const": 9}`, `"result_schema_version": {"const": 9}`, `"provider_invocations": {"enum": [1, 2]}`, `"DISMISSED"`, quality.RestrictedAdjudicationDropReason} {
		if !strings.Contains(text, required) {
			t.Errorf("v9 schema is missing %s", required)
		}
	}
}

func TestNativeResultV10SchemaCarriesStageAttemptAccounting(t *testing.T) {
	raw, err := Schema("review-result-v10.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{
		`"schema_version": {"const": 10}`,
		`"result_schema_version": {"const": 10}`,
		`"provider_invocations": {"type": "integer", "minimum": 1, "maximum": 3}`,
		`"native_attempts"`, `"restricted_attempts"`, `"provider_attempts_total"`,
		`"adopted_restricted_attempt"`, `"resumed"`, `"resumed_session_digest"`,
	} {
		if !strings.Contains(text, required) {
			t.Errorf("v10 schema is missing %s", required)
		}
	}
}

func TestV8V9AndEnvelopeV1V2SchemasRemainByteFrozen(t *testing.T) {
	for path, want := range map[string]string{
		"schemas/review-result-v8.schema.json":          "034f02916ddbee27d1bc6b9f6172ca7bff98dad751ddf73a02791388570efa9c",
		"schemas/review-result-v9.schema.json":          "bb0f8a7ee38a417e1c41b7011306ba01daf96406e371901e6ba959f2c7913eba",
		"schemas/review-result-envelope-v1.schema.json": "8cb3e7dad7e2b1c40fa1ade197572fdd910de505d9e6d90f508c59bb75a23145",
		"schemas/review-result-envelope-v2.schema.json": "6f1aa665add1489cdbec4b28594119292d0d8c2a25644ebf533c7f0b3b0a4936",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != want {
			t.Fatalf("immutable schema %s changed: %s", path, got)
		}
	}
}

func TestReviewSummarySchemaV3CarriesIdentityAndIncrementalCounts(t *testing.T) {
	raw, err := Schema("review-summary.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{`"schema_version": {"const": 3}`, `"review_scope"`, `"review_key"`, `"current_head"`, `"resolved_previous_findings"`, `"unresolved_previous_findings"`, `"new_findings"`, `"blocking_issues"`, `"advisory_issues"`} {
		if !strings.Contains(text, required) {
			t.Errorf("summary schema is missing %s", required)
		}
	}
}

func TestIncrementalProviderSchemaUsesStructuredOutputSubset(t *testing.T) {
	raw, err := Schema("native-review-incremental-output.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, unsupported := range []string{`"$schema"`, `"$ref"`, `"allOf"`, `"oneOf"`} {
		if strings.Contains(text, unsupported) {
			t.Fatalf("incremental provider schema contains unsupported construct %s", unsupported)
		}
	}
	for _, required := range []string{`"previous_finding_resolutions"`, `"new_findings"`, `"RESOLVED"`, `"UNRESOLVED"`, `"current_finding"`} {
		if !strings.Contains(text, required) {
			t.Errorf("incremental provider schema is missing %s", required)
		}
	}
}

func TestCompanyCIEnvelopeV1RemainsFrozen(t *testing.T) {
	for path, want := range map[string]string{
		"schemas/review-result-envelope-v1.schema.json":          "8cb3e7dad7e2b1c40fa1ade197572fdd910de505d9e6d90f508c59bb75a23145",
		"docs/company-ci-review-result-envelope-v1.example.json": "d2ef6b01551161a98b8bc7073528bf8c0b9c08675dcf52f1b1ab3f7525a46bf6",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != want {
			t.Fatalf("immutable envelope-v1 artifact %s changed: %s", path, got)
		}
	}
}

func TestCompanyCIEnvelopeV2KeepsLifecycleOutsideImmutableResult(t *testing.T) {
	raw, err := Schema("review-result-envelope-v2.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	schema := string(raw)
	for _, required := range []string{`"schema_version": {"const": 2}`, `"EXECUTED"`, `"REUSED"`, `"CURRENT"`, `"SUPERSEDED"`, `"$ref": "review-result-v9.schema.json"`} {
		if !strings.Contains(schema, required) {
			t.Errorf("company CI envelope schema is missing %s", required)
		}
	}

	fixture, err := os.ReadFile("docs/company-ci-review-result-envelope-v2.example.json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		SchemaVersion   int                        `json:"schema_version"`
		ResultSource    string                     `json:"result_source"`
		LifecycleStatus string                     `json:"lifecycle_status"`
		ReviewResult    quality.NativeReviewResult `json:"review_result"`
	}
	if err := json.Unmarshal(fixture, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != 2 || envelope.ResultSource != "EXECUTED" || envelope.LifecycleStatus != "CURRENT" {
		t.Fatalf("company CI envelope fixture drifted: %#v", envelope)
	}
	if envelope.ReviewResult.Contract.ToolVersion != quality.SkillVersion ||
		envelope.ReviewResult.Contract.ReasoningEffort != "max" ||
		envelope.ReviewResult.Execution.ReasoningEffort != "max" {
		t.Fatalf("company CI fixture does not use the current max-effort contract: %#v", envelope.ReviewResult.Contract)
	}
	if problems := quality.ValidateNativeResult(envelope.ReviewResult); len(problems) > 0 {
		t.Fatalf("embedded immutable review result is invalid: %#v", problems)
	}
	resultJSON, err := json.Marshal(envelope.ReviewResult)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(resultJSON), "result_source") || strings.Contains(string(resultJSON), "lifecycle_status") {
		t.Fatal("raw CLI result contains company CI lifecycle fields")
	}
}

func TestCompanyCIEnvelopeV3ReferencesOnlyResultV10(t *testing.T) {
	raw, err := Schema("review-result-envelope-v3.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{`"schema_version": {"const": 3}`, `"EXECUTED"`, `"REUSED"`, `"CURRENT"`, `"SUPERSEDED"`, `"$ref": "review-result-v10.schema.json"`} {
		if !strings.Contains(text, required) {
			t.Errorf("company CI envelope v3 is missing %s", required)
		}
	}
	if strings.Contains(text, "review-result-v9.schema.json") {
		t.Fatal("company CI envelope v3 still references result v9")
	}
}

func TestEmbeddedPolicyMatchesV12Contract(t *testing.T) {
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
	if policy.PolicyVersion != "1.2.0" || policy.Rubric != "policy/v1.2/rubric.md" || len(policy.Rules) != 20 || policy.AgentLimit != 2 {
		t.Fatalf("embedded policy contract drifted: %#v", policy)
	}
}

func TestModelReviewSchemaExposesFrozenDimensionAndRuleEnums(t *testing.T) {
	raw, err := Schema("model-review.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	definitions, ok := schema["$defs"].(map[string]any)
	if !ok {
		t.Fatal("model review schema is missing $defs")
	}
	dimension, ok := definitions["dimension"].(map[string]any)
	if !ok || !reflect.DeepEqual(dimension["enum"], []any{"D1", "D2", "D3", "D4"}) {
		t.Fatalf("model review dimension enum drifted: %#v", dimension)
	}
	ruleID, ok := definitions["rule_id"].(map[string]any)
	ruleIDs, idsOK := ruleID["enum"].([]any)
	policyRaw, policyErr := PolicyManifest()
	if policyErr != nil {
		t.Fatal(policyErr)
	}
	var policy quality.PolicyManifest
	if err := json.Unmarshal(policyRaw, &policy); err != nil {
		t.Fatal(err)
	}
	expectedRuleIDs := make([]any, 0, len(policy.Rules))
	for _, rule := range policy.Rules {
		expectedRuleIDs = append(expectedRuleIDs, rule.ID)
	}
	if !ok || !idsOK || !reflect.DeepEqual(ruleIDs, expectedRuleIDs) {
		t.Fatalf("model review rule ID enum drifted: %#v", ruleID)
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
		"review-result-v3.schema.json",
		"review-result-v4.schema.json",
		"review-result-v5.schema.json",
		"review-result-v6.schema.json",
		"review-result-v7.schema.json",
		"review-result-v8.schema.json",
		"review-result-v9.schema.json",
		"review-result-v10.schema.json",
		"review-result-envelope-v1.schema.json",
		"review-result-envelope-v2.schema.json",
		"review-result-envelope-v3.schema.json",
		"native-review-output.schema.json",
		"native-review-incremental-output.schema.json",
		"restricted-adjudication-output.schema.json",
		"restricted-adjudication-freeze.schema.json",
		"review-summary.schema.json",
		"native-review-freeze.schema.json",
		"native-run-metrics.schema.json",
		"native-stage-metrics-v2.schema.json",
		"native-session-checkpoint.schema.json",
		"restricted-attempt.schema.json",
	} {
		schema, err := Schema(name)
		if err != nil || len(schema) == 0 {
			t.Fatalf("embedded schema %s unavailable: %v", name, err)
		}
		if name == "model-review.schema.json" && strings.Contains(string(schema), `"$schema"`) {
			t.Fatal("Codex output schema uses an unsupported draft declaration")
		}
	}
	if lens, err := ReviewLens(); err != nil || !strings.Contains(string(lens), "不设固定数量上限") || strings.Contains(string(lens), "最多 3") {
		t.Fatalf("embedded review lens contract drifted: %v", err)
	}
}

func TestNativeMetricsSchemaRejectsAvailableAllZeroUsage(t *testing.T) {
	raw, err := Schema("native-run-metrics.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		AllOf []struct {
			Then struct {
				Not struct {
					Required []string `json:"required"`
				} `json:"not"`
				AnyOf []struct {
					Properties map[string]struct {
						Minimum int `json:"minimum"`
					} `json:"properties"`
				} `json:"anyOf"`
			} `json:"then"`
		} `json:"allOf"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.AllOf) != 1 || !reflect.DeepEqual(schema.AllOf[0].Then.Not.Required, []string{"usage_error"}) {
		t.Fatalf("available-usage branch does not forbid usage_error: %#v", schema.AllOf)
	}
	positiveCounters := map[string]int{}
	for _, alternative := range schema.AllOf[0].Then.AnyOf {
		for name, constraint := range alternative.Properties {
			positiveCounters[name] = constraint.Minimum
		}
	}
	if !reflect.DeepEqual(positiveCounters, map[string]int{"input_tokens": 1, "output_tokens": 1}) {
		t.Fatalf("positive usage constraints = %#v", positiveCounters)
	}
}

func TestNativeFreezeSchemaFixesArtifactIdentityAndDigest(t *testing.T) {
	raw, err := Schema("native-review-freeze.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	definitions, _ := schema["$defs"].(map[string]any)
	artifact, _ := definitions["artifact"].(map[string]any)
	conditions, _ := artifact["allOf"].([]any)
	if len(conditions) != 1 || !strings.Contains(fmt.Sprint(conditions[0]), "sha256") {
		t.Fatalf("artifact digest invariant is missing: %#v", artifact)
	}
	properties, _ := schema["properties"].(map[string]any)
	artifacts, _ := properties["artifacts"].(map[string]any)
	prefixItems, _ := artifacts["prefixItems"].([]any)
	if len(prefixItems) != 3 || artifacts["items"] != false {
		t.Fatalf("artifact identity sequence is not fixed: %#v", artifacts)
	}
	encoded := fmt.Sprint(prefixItems)
	for _, required := range []string{"final_message", "native-review.txt", "jsonl_stdout", "native-review.stdout.log", "stderr", "native-review.stderr.log"} {
		if !strings.Contains(encoded, required) {
			t.Errorf("fixed artifact sequence is missing %q", required)
		}
	}
}

func TestNativeRuntimeSchemasDoNotExposeRuleCoverageContract(t *testing.T) {
	for _, name := range []string{"review-result-v3.schema.json", "review-result-v4.schema.json", "review-result-v5.schema.json", "review-result-v6.schema.json", "review-result-v7.schema.json", "review-result-v8.schema.json", "review-result-v9.schema.json"} {
		raw, err := Schema(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, obsolete := range []string{"activated_rule_families", "inactive_rule_families", "rereview_scope", "rule_id", "directions", "verifier_status"} {
			if strings.Contains(string(raw), obsolete) {
				t.Fatalf("%s exposes obsolete runtime field %q", name, obsolete)
			}
		}
	}
	for _, retired := range []string{"candidate-verifier.schema.json", "review-result-v2.schema.json"} {
		if _, err := Schema(retired); err == nil {
			t.Fatalf("retired runtime schema %s is still embedded", retired)
		}
	}
}

func TestRestrictedAdjudicationPolicyHasOneEmbeddedTruthSource(t *testing.T) {
	raw, err := RestrictedAdjudicationPolicy()
	if err != nil {
		t.Fatal(err)
	}
	policy := string(raw)
	for _, required := range []string{"candidate-only production-floor adjudicator", "Do not perform another general code review", "SUPPORTED", "S3", "T3", "E2 or E3", "recommended disposition is advisory"} {
		if !strings.Contains(policy, required) {
			t.Errorf("restricted adjudication policy is missing %q", required)
		}
	}
	if _, err := os.Stat("pilot/restricted-adjudication-policy.md"); !os.IsNotExist(err) {
		t.Fatalf("pilot policy duplicate still exists: %v", err)
	}
}

func TestEmbeddedRubricMatchesV1RuntimeAgentContract(t *testing.T) {
	raw, err := Rubric()
	if err != nil {
		t.Fatal(err)
	}
	rubric := string(raw)
	for _, expected := range []string{"当前 report-only 只使用 1 个主 Agent", "总 Agent 数硬上限 2"} {
		if !strings.Contains(rubric, expected) {
			t.Fatalf("embedded rubric is missing %q", expected)
		}
	}
	for _, retired := range []string{"最多 3 个 Agent", "超过 3 个", "每个候选最多交给一个独立验证 Agent"} {
		if strings.Contains(rubric, retired) {
			t.Fatalf("embedded rubric retains retired runtime guidance %q", retired)
		}
	}
}

func TestEmbeddedWorkflowUsesOrdinarySingleAgentReview(t *testing.T) {
	raw, err := Workflow()
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)
	for _, expected := range []string{"ordinary diff-first review", "Each finding needs only", "Do not start a subagent", "no fixed limit", "rereview_scope", "REVIEW_INVALID", "validation_errors"} {
		if !strings.Contains(workflow, expected) {
			t.Fatalf("embedded workflow is missing %q", expected)
		}
	}
	for _, retired := range []string{"Optional batch verifier", "Start exactly one read-only subagent", "at most the three"} {
		if strings.Contains(workflow, retired) {
			t.Fatalf("embedded workflow retains retired guidance %q", retired)
		}
	}
}

func TestEmbeddedWorkflowMinimalReviewExampleDecodes(t *testing.T) {
	raw, err := Workflow()
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "Minimal valid output: `"
	workflow := string(raw)
	start := strings.Index(workflow, prefix)
	if start < 0 {
		t.Fatal("workflow is missing its minimal valid output example")
	}
	example := workflow[start+len(prefix):]
	end := strings.Index(example, "`")
	if end < 0 {
		t.Fatal("workflow minimal valid output example is not terminated")
	}
	if _, err := quality.DecodeModelReview(strings.NewReader(example[:end])); err != nil {
		t.Fatalf("workflow minimal valid output does not decode: %v", err)
	}
}
