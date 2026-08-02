package quality

import (
	"strings"
	"testing"
)

func TestRenderNativeMarkdownDirectsDocumentLevelManualReviewToFrozenOutput(t *testing.T) {
	result := validNativeResult()
	result.Findings = []NativeFinding{}
	result.Adjudication.Reasons = []string{"inspect frozen native-review.txt"}

	markdown := RenderNativeMarkdown(result)
	if !strings.Contains(markdown, "native-review.txt") {
		t.Fatalf("manual review report does not reference frozen output:\n%s", markdown)
	}
	if strings.Contains(markdown, "No actionable finding remained.") {
		t.Fatalf("manual review report claims there are no findings:\n%s", markdown)
	}
}
