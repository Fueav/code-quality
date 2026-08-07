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
