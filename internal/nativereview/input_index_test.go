package nativereview

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Fueav/code-quality/internal/reviewplan"
	"github.com/Fueav/code-quality/quality"
)

func TestFrozenInputIndexDistinguishesFullAndProviderRanges(t *testing.T) {
	for _, scope := range []string{quality.ReviewScopeFull, quality.ReviewScopeIncremental} {
		t.Run(scope, func(t *testing.T) {
			plan := reviewplan.Decision{
				ReviewIdentity:  quality.ReviewIdentity{ReviewScope: scope},
				Request:         quality.ReviewRequest{BaseCommit: "full-base", TargetCommit: "head", ChangedFiles: []string{"old.go", "repair.go"}},
				ProviderRequest: quality.ReviewRequest{BaseCommit: "full-base", TargetCommit: "head", ChangedFiles: []string{"old.go", "repair.go"}},
			}
			if scope == quality.ReviewScopeIncremental {
				plan.ProviderRequest.BaseCommit = "previous-head"
				plan.ProviderRequest.ChangedFiles = []string{"repair.go"}
			}
			indexText := frozenInputIndexPrompt(plan)
			var index frozenInputIndex
			if err := json.Unmarshal([]byte(strings.Split(indexText, "\n")[2]), &index); err != nil {
				t.Fatal(err)
			}
			if index.FullScope.BaseCommit != "full-base" || index.FullScope.TargetCommit != "head" || len(index.FullScope.ChangedFiles) != 2 ||
				index.ProviderRequestPath != "../review-request.json" || index.ProviderScope.DiffPath != "../trusted.diff" {
				t.Fatalf("frozen input index = %#v", index)
			}
			if scope == quality.ReviewScopeIncremental {
				if index.FullScope.DiffPath != "" || index.ProviderScope.BaseCommit != "previous-head" || len(index.ProviderScope.ChangedFiles) != 1 {
					t.Fatalf("incremental diff misrepresented as the full change: %#v", index)
				}
			} else if index.FullScope.DiffPath != index.ProviderScope.DiffPath {
				t.Fatal("FULL should share the existing frozen diff")
			}
			for _, prompt := range []string{
				buildPlanReviewPrompt(plan, "", true),
				buildRestrictedAdjudicationPrompt(plan, nil),
			} {
				if strings.Count(prompt, indexText) != 1 {
					t.Fatalf("stage did not receive the shared index exactly once: %s", prompt)
				}
			}
		})
	}
}

func TestCheckpointBindsActualProviderPromptIncludingInputIndex(t *testing.T) {
	for _, provider := range []Provider{NewCodexProvider(""), NewClaudeProvider("")} {
		t.Run(provider.Host(), func(t *testing.T) {
			session, _ := nativeFixture(t)
			options := nativeRunOptions{Session: session, Provider: provider}
			if err := normalizeRunOptions(&options); err != nil {
				t.Fatal(err)
			}
			invocation := buildReviewInvocation(options)
			prompt := invocation.stdin
			if provider.Host() == "claude-code" {
				prompt = invocation.args[len(invocation.args)-1]
			}
			checkpoint, err := newSessionCheckpoint(session, options.Plan, "")
			if err != nil {
				t.Fatal(err)
			}
			if checkpoint.ContractArtifacts.NativePromptSHA256 != quality.SHA256Digest([]byte(prompt)) {
				t.Fatal("checkpoint prompt digest differs from the actual Provider prompt")
			}
			if !strings.Contains(prompt, frozenInputIndexPrompt(options.Plan)) {
				t.Fatal("Provider prompt omitted the frozen input index")
			}
		})
	}
}
