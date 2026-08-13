package quality

import (
	"strings"
	"testing"
)

func TestRenderNativeMarkdownShowsBlockingDecisionFirst(t *testing.T) {
	result := validNativeResult()

	markdown := RenderNativeMarkdown(result)
	for _, want := range []string{"BLOCK", "HOLD", "Blocking issues: 1", "wrong value", "Return the expected value"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("release summary is missing %q:\n%s", want, markdown)
		}
	}
}

func TestRenderNativeMarkdownSeparatesAdvisories(t *testing.T) {
	result := validNativeResult()
	result.Findings[0].Priority = 2
	result.Adjudication.SemanticResult = ResultPass
	result.Adjudication.CIAction = "continue_release"

	markdown := RenderNativeMarkdown(result)
	for _, want := range []string{"PASS", "CONTINUE", "Blocking issues: 0", "Advisory issues: 1", "## Advisories", "wrong value"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("release summary is missing %q:\n%s", want, markdown)
		}
	}
	if strings.Contains(markdown, "## Issues to fix before release") {
		t.Fatalf("advisory-only summary contains a blocking section:\n%s", markdown)
	}
}
