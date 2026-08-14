package nativereview

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Fueav/code-quality/internal/reviewplan"
	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

func TestRestrictedDecisionIgnoresModelRecommendation(t *testing.T) {
	session, _ := nativeFixture(t)
	finding := restrictedFixtureFinding(t)
	payload := restrictedFixturePayload(finding.ID, "REJECT", "app.go")
	decisions, err := decodeRestrictedAdjudication([]byte(payload), []quality.NativeFinding{finding}, session.RepositoryDirectory())
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || !decisions[0].Retain {
		t.Fatalf("trusted code followed the model recommendation: %#v", decisions)
	}
}

func restrictedFixturePlan(session reviewsession.NativeSession) reviewplan.Decision {
	request := session.Request()
	return reviewplan.Decision{
		ReviewIdentity: quality.ReviewIdentity{ReviewScope: quality.ReviewScopeFull},
		Request:        request, ProviderRequest: request,
	}
}

func TestRestrictedDecisionCannotBlockOnUnsafeEvidenceReference(t *testing.T) {
	session, _ := nativeFixture(t)
	finding := restrictedFixtureFinding(t)
	payload := restrictedFixturePayload(finding.ID, "BLOCK", "../outside.go")
	decisions, err := decodeRestrictedAdjudication([]byte(payload), []quality.NativeFinding{finding}, session.RepositoryDirectory())
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].Retain {
		t.Fatalf("unsafe evidence retained a blocker: %#v", decisions)
	}
}

func TestRestrictedDecisionRequiresExactFrozenIDsAndFields(t *testing.T) {
	session, _ := nativeFixture(t)
	finding := restrictedFixtureFinding(t)
	wrongID := "finding-v1:sha256:" + fmt.Sprintf("%064d", 1)
	for name, payload := range map[string]string{
		"wrong id":     restrictedFixturePayload(wrongID, "BLOCK", "app.go"),
		"extra field":  `{"adjudications":[{"finding_id":"` + finding.ID + `","extra":true}]}`,
		"missing item": `{"adjudications":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeRestrictedAdjudication([]byte(payload), []quality.NativeFinding{finding}, session.RepositoryDirectory()); err == nil {
				t.Fatal("invalid restricted payload was accepted")
			}
		})
	}
}

func TestRestrictedIncrementalPromptPreservesFullScopeAndFinalRound(t *testing.T) {
	finding := restrictedFixtureFinding(t)
	plan := reviewplan.Decision{
		ReviewIdentity:  quality.ReviewIdentity{ReviewScope: quality.ReviewScopeIncremental},
		Request:         quality.ReviewRequest{BaseCommit: "full-base", TargetCommit: "current-head"},
		ProviderRequest: quality.ReviewRequest{BaseCommit: "previous-head", TargetCommit: "current-head"},
	}
	prompt := buildRestrictedAdjudicationPrompt(plan, []quality.NativeFinding{finding})
	for _, required := range []string{"full-base..current-head", "previous-head..current-head", "second and final automatic review round", finding.ID} {
		if !strings.Contains(prompt, required) {
			t.Errorf("incremental restricted prompt is missing %q: %s", required, prompt)
		}
	}
}

func restrictedFixtureFinding(t *testing.T) quality.NativeFinding {
	t.Helper()
	finding, err := quality.IdentifyNativeFinding(quality.NativeFinding{
		Title: "deterministic outage", Priority: 1, Reason: "The changed entry always exhausts the worker.",
		Suggestion: "Bound the changed loop.", CodeLocation: quality.NativeCodeLocation{Path: "app.go", StartLine: 2, EndLine: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	return finding
}

func restrictedFixturePayload(id, recommendation, path string) string {
	return fmt.Sprintf(`{"adjudications":[{"finding_id":%q,"validity":"SUPPORTED","severity":"S3","trigger_confidence":"T3","evidence_level":"E2","introduced_or_worsened_by_change":true,"trigger_condition_is_concrete":true,"causal_chain_is_complete":true,"finding_is_not_style_preference":true,"recommended_disposition":%q,"evidence_refs":[{"path":%q,"start_line":2,"end_line":2,"support":"The committed entry proves the path."}],"uncertainties":[],"reason":"The repository proves the full causal chain."}]}`, id, recommendation, path)
}
