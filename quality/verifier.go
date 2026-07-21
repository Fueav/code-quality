package quality

import (
	"fmt"
	"sort"
	"strings"
)

type VerifierRequest struct {
	SchemaVersion int           `json:"schema_version"`
	Request       ReviewRequest `json:"request"`
	Candidates    []Finding     `json:"candidates"`
}

func ValidateMainReview(review ModelReview, policy PolicyManifest) []string {
	errors := ValidateModelReview(review, policy)
	for index, finding := range review.Findings {
		if finding.VerifierResult != "not_run" {
			errors = append(errors, fmt.Sprintf("findings[%d].verifier_result must be not_run in the main review", index))
		}
	}
	return uniqueSorted(errors)
}

func PotentialBlockFindings(review ModelReview) []Finding {
	candidates := make([]Finding, 0, len(review.Findings))
	for _, finding := range review.Findings {
		if finding.VerifierResult == "not_run" && satisfiesBlockFormula(finding) {
			candidates = append(candidates, finding)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].ID < candidates[j].ID
	})
	return candidates
}

func BuildVerifierRequest(request ReviewRequest, review ModelReview) VerifierRequest {
	return VerifierRequest{
		SchemaVersion: 1,
		Request:       request,
		Candidates:    PotentialBlockFindings(review),
	}
}

func ValidateVerifierReview(review VerifierReview, candidates []Finding) []string {
	var errors []string
	candidateIDs := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidateIDs[candidate.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(review.Decisions))
	for index, decision := range review.Decisions {
		prefix := fmt.Sprintf("decisions[%d]", index)
		if _, ok := candidateIDs[decision.FindingID]; !ok {
			errors = append(errors, prefix+".finding_id is not a potential BLOCK candidate")
		}
		if _, exists := seen[decision.FindingID]; exists {
			errors = append(errors, prefix+".finding_id is duplicated")
		}
		seen[decision.FindingID] = struct{}{}
		if decision.Result != "confirmed" && decision.Result != "refuted" && decision.Result != "insufficient" {
			errors = append(errors, prefix+".result is invalid")
		}
		if strings.TrimSpace(decision.VerificationSummary) == "" {
			errors = append(errors, prefix+".verification_summary is required")
		}
		if containsBlank(decision.Uncertainties) {
			errors = append(errors, prefix+".uncertainties contains an empty value")
		}
	}
	for id := range candidateIDs {
		if _, exists := seen[id]; !exists {
			errors = append(errors, "verifier decision is missing for "+id)
		}
	}
	return uniqueSorted(errors)
}

func MergeVerifierReview(main ModelReview, verifier VerifierReview) ModelReview {
	decisions := make(map[string]VerifierDecision, len(verifier.Decisions))
	for _, decision := range verifier.Decisions {
		decisions[decision.FindingID] = decision
	}
	for index := range main.Findings {
		decision, exists := decisions[main.Findings[index].ID]
		if !exists {
			continue
		}
		main.Findings[index].VerifierResult = decision.Result
		main.Findings[index].VerificationPerformed = append(
			main.Findings[index].VerificationPerformed,
			"Verifier: "+decision.VerificationSummary,
		)
		main.Findings[index].Uncertainties = append(
			main.Findings[index].Uncertainties,
			decision.Uncertainties...,
		)
	}
	return main
}
