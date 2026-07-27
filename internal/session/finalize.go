package session

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Fueav/code-quality/quality"
)

const maxReviewBytes = int64(10 << 20)

type FinalizeOptions struct {
	SessionDir string
}

type Finalized struct {
	SchemaVersion         int    `json:"schema_version"`
	Status                string `json:"status"`
	SessionDir            string `json:"session_dir"`
	RepositoryDir         string `json:"repository_dir,omitempty"`
	DiffPath              string `json:"diff_path,omitempty"`
	RubricPath            string `json:"rubric_path,omitempty"`
	ResultPath            string `json:"result_path,omitempty"`
	MarkdownPath          string `json:"markdown_path,omitempty"`
	SemanticResult        string `json:"semantic_result,omitempty"`
	ReviewRequired        bool   `json:"review_required"`
	CompletedReviewRounds int    `json:"completed_review_rounds"`
	MaximumReviewRounds   int    `json:"maximum_review_rounds"`
	NextReviewPath        string `json:"next_review_path,omitempty"`
}

func Finalize(options FinalizeOptions, policy quality.PolicyManifest) (Finalized, error) {
	layout := NewLayout(options.SessionDir)
	if err := ValidateLayout(layout); err != nil {
		return Finalized{}, err
	}
	requestRaw, err := ReadRegularFile(layout.RequestPath, maxReviewBytes)
	if err != nil {
		return Finalized{}, fmt.Errorf("read review request: %w", err)
	}
	request, err := quality.DecodeStrict[quality.ReviewRequest](bytes.NewReader(requestRaw))
	if err != nil {
		return Finalized{}, fmt.Errorf("decode review request: %w", err)
	}
	metadataRaw, err := ReadRegularFile(layout.MetadataPath, maxReviewBytes)
	if err != nil {
		return writeIncomplete(layout, "", request, policy, quality.Execution{}, 0, "read session metadata: "+err.Error())
	}
	metadata, err := quality.DecodeStrict[Metadata](bytes.NewReader(metadataRaw))
	if err != nil || metadata.SchemaVersion != 1 || (metadata.Host != "claude-code" && metadata.Host != "codex") {
		return writeIncomplete(layout, "", request, policy, quality.Execution{}, 0, "session metadata is invalid")
	}
	if metadata.SkillVersion != quality.SkillVersion {
		return writeIncomplete(layout, "", request, policy, quality.Execution{}, 0,
			fmt.Sprintf("quality-review binary version changed between prepare (%q) and finalize (%q); use the same binary version for both steps", metadata.SkillVersion, quality.SkillVersion))
	}
	execution := quality.Execution{Host: metadata.Host, SkillVersion: metadata.SkillVersion, AgentCount: 1}
	mainRaw, err := ReadRegularFile(layout.MainReviewPath, maxReviewBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return writeIncomplete(layout, metadata.RepositoryRoot, request, policy, execution, 0, "main review is missing")
		}
		return writeIncomplete(layout, metadata.RepositoryRoot, request, policy, execution, 0, "read main review: "+err.Error())
	}
	main, err := quality.DecodeModelReview(bytes.NewReader(mainRaw))
	if err != nil {
		return writeIncomplete(layout, metadata.RepositoryRoot, request, policy, execution, 0, "invalid main review: "+err.Error())
	}
	main.Execution = execution
	firstResult := quality.Adjudicate(request, main, policy)
	if firstResult.Adjudication.SemanticResult != quality.ResultPass || len(firstResult.Findings) != 0 {
		main.Execution.RetryCount = intPointer(0)
		return writeComplete(layout, metadata.RepositoryRoot, request, main, policy, 1)
	}
	rereviewRaw, err := ReadRegularFile(layout.RereviewPath, maxReviewBytes)
	if errors.Is(err, os.ErrNotExist) {
		return Finalized{
			SchemaVersion: 1, Status: "REREVIEW_REQUIRED", SessionDir: layout.SessionDir,
			RepositoryDir: layout.RepositoryDir, DiffPath: layout.DiffPath, RubricPath: layout.RubricPath,
			ReviewRequired: true, CompletedReviewRounds: 1, MaximumReviewRounds: 2, NextReviewPath: layout.RereviewPath,
		}, nil
	}
	if err != nil {
		return writeIncomplete(layout, metadata.RepositoryRoot, request, policy, execution, 1, "read rereview: "+err.Error())
	}
	rereview, err := quality.DecodeModelReview(bytes.NewReader(rereviewRaw))
	if err != nil {
		return writeIncomplete(layout, metadata.RepositoryRoot, request, policy, execution, 1, "invalid rereview: "+err.Error())
	}
	merged := mergeReviews(main, rereview)
	merged.Execution = execution
	merged.Execution.RetryCount = intPointer(1)
	return writeComplete(layout, metadata.RepositoryRoot, request, merged, policy, 2)
}

func writeComplete(layout Layout, repositoryRoot string, request quality.ReviewRequest, review quality.ModelReview, policy quality.PolicyManifest, reviewRounds int) (Finalized, error) {
	result := quality.Adjudicate(request, review, policy)
	if err := writeReports(layout, result); err != nil {
		return Finalized{}, err
	}
	cleanupWorktree(repositoryRoot, layout.RepositoryDir)
	return Finalized{
		SchemaVersion:         1,
		Status:                "COMPLETE",
		SessionDir:            layout.SessionDir,
		ResultPath:            layout.ResultPath,
		MarkdownPath:          layout.MarkdownPath,
		SemanticResult:        result.Adjudication.SemanticResult,
		ReviewRequired:        false,
		CompletedReviewRounds: reviewRounds,
		MaximumReviewRounds:   2,
	}, nil
}

func writeIncomplete(layout Layout, repositoryRoot string, request quality.ReviewRequest, policy quality.PolicyManifest, execution quality.Execution, completedReviewRounds int, reasons ...string) (Finalized, error) {
	result := quality.IncompleteResultWithExecution(request, policy, execution, reasons...)
	if err := writeReports(layout, result); err != nil {
		return Finalized{}, err
	}
	cleanupWorktree(repositoryRoot, layout.RepositoryDir)
	return Finalized{
		SchemaVersion:         1,
		Status:                "INCOMPLETE",
		SessionDir:            layout.SessionDir,
		ResultPath:            layout.ResultPath,
		MarkdownPath:          layout.MarkdownPath,
		SemanticResult:        quality.ResultIncomplete,
		ReviewRequired:        false,
		CompletedReviewRounds: completedReviewRounds,
		MaximumReviewRounds:   2,
	}, nil
}

func mergeReviews(first, second quality.ModelReview) quality.ModelReview {
	merged := quality.ModelReview{
		ActivatedRuleFamilies: uniqueStrings(append(append([]string{}, second.ActivatedRuleFamilies...), first.ActivatedRuleFamilies...)),
		InactiveRuleFamilies:  mergeInactiveFamilies(second.InactiveRuleFamilies, first.InactiveRuleFamilies),
		Findings:              mergeFindings(second.Findings, first.Findings),
		UninspectedScope:      uniqueStrings(append(append([]string{}, second.UninspectedScope...), first.UninspectedScope...)),
		MissingContext:        uniqueStrings(append(append([]string{}, second.MissingContext...), first.MissingContext...)),
		InspectedContext:      mergeInspectedContext(second.InspectedContext, first.InspectedContext),
	}
	return merged
}

func mergeFindings(groups ...[]quality.Finding) []quality.Finding {
	merged := []quality.Finding{}
	seenRoots := map[string]struct{}{}
	seenIDs := map[string]struct{}{}
	for groupIndex, findings := range groups {
		for _, finding := range findings {
			key := findingRootKey(finding)
			if _, exists := seenRoots[key]; exists {
				continue
			}
			if _, exists := seenIDs[finding.ID]; exists {
				baseID := fmt.Sprintf("%s-R%d", finding.ID, len(groups)-groupIndex)
				finding.ID = baseID
				for suffix := 2; ; suffix++ {
					if _, collision := seenIDs[finding.ID]; !collision {
						break
					}
					finding.ID = fmt.Sprintf("%s-%d", baseID, suffix)
				}
			}
			seenRoots[key] = struct{}{}
			seenIDs[finding.ID] = struct{}{}
			merged = append(merged, finding)
		}
	}
	return merged
}

func findingRootKey(finding quality.Finding) string {
	locations := make([]string, 0, len(finding.CodeLocations))
	for _, location := range finding.CodeLocations {
		locations = append(locations, fmt.Sprintf("%s:%d", location.Path, location.Line))
	}
	sort.Strings(locations)
	return finding.RuleID + "|" + strings.Join(locations, ",")
}

func mergeInactiveFamilies(groups ...[]quality.InactiveRuleFamily) []quality.InactiveRuleFamily {
	merged := []quality.InactiveRuleFamily{}
	seen := map[string]struct{}{}
	for _, families := range groups {
		for _, family := range families {
			if _, exists := seen[family.ID]; exists {
				continue
			}
			seen[family.ID] = struct{}{}
			merged = append(merged, family)
		}
	}
	return merged
}

func mergeInspectedContext(groups ...[]quality.InspectedContext) []quality.InspectedContext {
	merged := []quality.InspectedContext{}
	seen := map[string]struct{}{}
	for _, contexts := range groups {
		for _, context := range contexts {
			if _, exists := seen[context.Path]; exists {
				continue
			}
			seen[context.Path] = struct{}{}
			merged = append(merged, context)
		}
	}
	return merged
}

func uniqueStrings(values []string) []string {
	result := []string{}
	seen := map[string]struct{}{}
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func intPointer(value int) *int {
	return &value
}

func cleanupWorktree(repositoryRoot, worktreePath string) {
	if strings.TrimSpace(repositoryRoot) == "" {
		return
	}
	removeWorktree(repositoryRoot, worktreePath)
}

func writeReports(layout Layout, result quality.ReviewResult) error {
	if err := ValidateLayout(layout); err != nil {
		return err
	}
	if err := writeAtomically(layout.OutputDir, layout.ResultPath, func(file *os.File) error {
		return quality.EncodeJSON(file, result)
	}); err != nil {
		return err
	}
	return writeAtomically(layout.OutputDir, layout.MarkdownPath, func(file *os.File) error {
		_, err := file.WriteString(quality.RenderMarkdown(result))
		return err
	})
}

func writeAtomically(directory, destination string, write func(*os.File) error) error {
	file, err := os.CreateTemp(directory, ".quality-review-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := write(file); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, destination)
}
