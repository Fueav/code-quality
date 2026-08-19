package quality

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	contractDigestPrefix = "contract-v1:sha256:"
	reviewKeyPrefix      = "review-v1:sha256:"
	findingIDPrefix      = "finding-v1:sha256:"
)

var sha256ValuePattern = regexp.MustCompile(`^(?:sha256:|contract-v1:sha256:|review-v1:sha256:|finding-v1:sha256:|session-v1:sha256:)[0-9a-f]{64}$`)

type NativeReviewContract struct {
	ToolVersion             string `json:"tool_version"`
	ResultSchemaVersion     int    `json:"result_schema_version"`
	ProviderOutputSchema    string `json:"provider_output_schema"`
	PromptContractVersion   string `json:"prompt_contract_version"`
	EvaluationRubricVersion string `json:"evaluation_rubric_version"`
	EvaluationRubricDigest  string `json:"evaluation_rubric_digest"`
	RestrictedPolicyDigest  string `json:"restricted_policy_digest"`
	RestrictedSchemaDigest  string `json:"restricted_schema_digest"`
	ProviderHost            string `json:"provider_host"`
	Model                   string `json:"model"`
	ReasoningEffort         string `json:"reasoning_effort"`
	ExecutionProfile        string `json:"execution_profile"`
}

type ReviewIdentity struct {
	ReviewKey         string               `json:"review_key"`
	ContractDigest    string               `json:"contract_digest"`
	ReviewScope       string               `json:"review_scope"`
	BaseRef           string               `json:"base_ref"`
	HeadRef           string               `json:"head_ref"`
	BaseTipCommit     string               `json:"base_tip_commit"`
	MergeBase         string               `json:"merge_base"`
	ParentReviewKey   *string              `json:"parent_review_key"`
	PreviousHead      *string              `json:"previous_head"`
	CurrentHead       string               `json:"current_head"`
	DeltaChangedFiles []string             `json:"delta_changed_files"`
	Contract          NativeReviewContract `json:"contract"`
}

type ReviewIdentityInput struct {
	Contract          NativeReviewContract
	Request           ReviewRequest
	ReviewGoal        string
	ReviewScope       string
	BaseRef           string
	HeadRef           string
	BaseTipCommit     string
	MergeBase         string
	ParentReviewKey   *string
	PreviousHead      *string
	CurrentHead       string
	DeltaChangedFiles []string
}

func BuildReviewIdentity(input ReviewIdentityInput) (ReviewIdentity, error) {
	input.ReviewGoal = strings.TrimSpace(input.ReviewGoal)
	input.BaseRef = strings.TrimSpace(input.BaseRef)
	input.HeadRef = strings.TrimSpace(input.HeadRef)
	input.BaseTipCommit = strings.TrimSpace(input.BaseTipCommit)
	input.MergeBase = strings.TrimSpace(input.MergeBase)
	input.CurrentHead = strings.TrimSpace(input.CurrentHead)
	input.Request.ChangedFiles = sortedUniqueCopy(input.Request.ChangedFiles)
	input.DeltaChangedFiles = sortedUniqueCopy(input.DeltaChangedFiles)
	if problems := validateReviewIdentityInput(input); len(problems) > 0 {
		return ReviewIdentity{}, errors.New(strings.Join(problems, "; "))
	}
	contractDigest, err := BuildNativeReviewContractDigest(input.Contract)
	if err != nil {
		return ReviewIdentity{}, err
	}
	keyInput := struct {
		Repository        string   `json:"repository"`
		BaseRef           string   `json:"base_ref"`
		HeadRef           string   `json:"head_ref"`
		BaseTipCommit     string   `json:"base_tip_commit"`
		MergeBase         string   `json:"merge_base"`
		BaseCommit        string   `json:"base_commit"`
		CurrentHead       string   `json:"current_head"`
		ChangedFiles      []string `json:"changed_files"`
		ContractDigest    string   `json:"contract_digest"`
		ReviewGoal        string   `json:"review_goal"`
		ReviewScope       string   `json:"review_scope"`
		ParentReviewKey   *string  `json:"parent_review_key"`
		PreviousHead      *string  `json:"previous_head"`
		DeltaChangedFiles []string `json:"delta_changed_files"`
	}{
		Repository: input.Request.Repository, BaseRef: input.BaseRef, HeadRef: input.HeadRef,
		BaseTipCommit: input.BaseTipCommit, MergeBase: input.MergeBase, BaseCommit: input.Request.BaseCommit,
		CurrentHead: input.CurrentHead, ChangedFiles: input.Request.ChangedFiles,
		ContractDigest: contractDigest, ReviewGoal: input.ReviewGoal, ReviewScope: input.ReviewScope,
		ParentReviewKey: cloneStringPointer(input.ParentReviewKey), PreviousHead: cloneStringPointer(input.PreviousHead),
		DeltaChangedFiles: input.DeltaChangedFiles,
	}
	reviewKey, err := hashCanonical(reviewKeyPrefix, keyInput)
	if err != nil {
		return ReviewIdentity{}, err
	}
	return ReviewIdentity{
		ReviewKey: reviewKey, ContractDigest: contractDigest, ReviewScope: input.ReviewScope,
		BaseRef: input.BaseRef, HeadRef: input.HeadRef, BaseTipCommit: input.BaseTipCommit,
		MergeBase: input.MergeBase, ParentReviewKey: cloneStringPointer(input.ParentReviewKey),
		PreviousHead: cloneStringPointer(input.PreviousHead), CurrentHead: input.CurrentHead,
		DeltaChangedFiles: input.DeltaChangedFiles, Contract: input.Contract,
	}, nil
}

func BuildNativeReviewContractDigest(contract NativeReviewContract) (string, error) {
	if problems := validateNativeReviewContract(contract); len(problems) > 0 {
		return "", errors.New(strings.Join(problems, "; "))
	}
	return hashCanonical(contractDigestPrefix, contract)
}

func RecomputeReviewIdentity(result NativeReviewResult) (ReviewIdentity, error) {
	return BuildReviewIdentity(ReviewIdentityInput{
		Contract: result.Contract, Request: result.Request, ReviewGoal: result.ReviewGoal,
		ReviewScope: result.ReviewScope, BaseRef: result.BaseRef, HeadRef: result.HeadRef,
		BaseTipCommit: result.BaseTipCommit, MergeBase: result.MergeBase,
		ParentReviewKey: result.ParentReviewKey, PreviousHead: result.PreviousHead,
		CurrentHead: result.CurrentHead, DeltaChangedFiles: result.DeltaChangedFiles,
	})
}

func IdentifyNativeFinding(finding NativeFinding) (NativeFinding, error) {
	finding.ID = ""
	finding.Title = strings.TrimSpace(finding.Title)
	finding.CodeLocation.Path = strings.TrimSpace(finding.CodeLocation.Path)
	finding.Reason = strings.TrimSpace(finding.Reason)
	finding.Suggestion = strings.TrimSpace(finding.Suggestion)
	if problems := validateNativeFinding(finding, nil, "finding"); len(problems) > 0 {
		return NativeFinding{}, errors.New(strings.Join(problems, "; "))
	}
	identityInput := struct {
		Priority     int                `json:"priority"`
		Title        string             `json:"title"`
		CodeLocation NativeCodeLocation `json:"code_location"`
		Reason       string             `json:"reason"`
		Suggestion   string             `json:"suggestion"`
	}{finding.Priority, finding.Title, finding.CodeLocation, finding.Reason, finding.Suggestion}
	id, err := hashCanonical(findingIDPrefix, identityInput)
	if err != nil {
		return NativeFinding{}, err
	}
	finding.ID = id
	return finding, nil
}

func validateReviewIdentityInput(input ReviewIdentityInput) []string {
	var problems []string
	if validation := ValidateRequest(input.Request); len(validation) > 0 {
		problems = append(problems, validation...)
	}
	if len(input.ReviewGoal) > 4000 {
		problems = append(problems, "review goal exceeds 4000 bytes")
	}
	for name, value := range map[string]string{
		"base_ref": input.BaseRef, "head_ref": input.HeadRef, "base_tip_commit": input.BaseTipCommit,
		"merge_base": input.MergeBase, "current_head": input.CurrentHead,
	} {
		if value == "" {
			problems = append(problems, name+" is required")
		}
	}
	if input.Request.TargetCommit != input.CurrentHead {
		problems = append(problems, "request target_commit must equal current_head")
	}
	problems = append(problems, validateNativeReviewContract(input.Contract)...)
	switch input.ReviewScope {
	case ReviewScopeFull:
		if input.ParentReviewKey != nil || input.PreviousHead != nil || len(input.DeltaChangedFiles) != 0 {
			problems = append(problems, "FULL identity cannot contain incremental lineage")
		}
	case ReviewScopeIncremental:
		if input.ParentReviewKey == nil || input.PreviousHead == nil || len(input.DeltaChangedFiles) == 0 {
			problems = append(problems, "INCREMENTAL identity requires parent key, previous head, and non-empty delta")
		} else {
			if !strings.HasPrefix(*input.ParentReviewKey, reviewKeyPrefix) {
				problems = append(problems, "parent review key is invalid")
			}
			if strings.TrimSpace(*input.PreviousHead) == "" || *input.PreviousHead == input.CurrentHead {
				problems = append(problems, "previous head must differ from current head")
			}
		}
	default:
		problems = append(problems, "review scope must be FULL or INCREMENTAL")
	}
	return uniqueSorted(problems)
}

func validateNativeReviewContract(contract NativeReviewContract) []string {
	var problems []string
	for name, value := range map[string]string{
		"tool_version": contract.ToolVersion, "provider_output_schema": contract.ProviderOutputSchema,
		"prompt_contract_version":   contract.PromptContractVersion,
		"evaluation_rubric_version": contract.EvaluationRubricVersion,
		"evaluation_rubric_digest":  contract.EvaluationRubricDigest,
		"restricted_policy_digest":  contract.RestrictedPolicyDigest,
		"restricted_schema_digest":  contract.RestrictedSchemaDigest,
		"provider_host":             contract.ProviderHost, "model": contract.Model,
		"reasoning_effort": contract.ReasoningEffort, "execution_profile": contract.ExecutionProfile,
	} {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, "contract "+name+" is required")
		}
	}
	if contract.ResultSchemaVersion != NativeResultSchemaVersion {
		problems = append(problems, fmt.Sprintf("contract result_schema_version must be %d", NativeResultSchemaVersion))
	}
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(contract.ProviderOutputSchema) {
		problems = append(problems, "contract provider_output_schema must be a SHA-256 digest")
	}
	for name, value := range map[string]string{
		"evaluation_rubric_digest": contract.EvaluationRubricDigest,
		"restricted_policy_digest": contract.RestrictedPolicyDigest,
		"restricted_schema_digest": contract.RestrictedSchemaDigest,
	} {
		if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(value) {
			problems = append(problems, "contract "+name+" must be a SHA-256 digest")
		}
	}
	if contract.ProviderHost != "codex" && contract.ProviderHost != "claude-code" {
		problems = append(problems, "contract provider_host is unsupported")
	}
	if contract.ExecutionProfile != ExecutionProfilePersonal && contract.ExecutionProfile != ExecutionProfileProductionCI {
		problems = append(problems, "contract execution_profile is unsupported")
	}
	return uniqueSorted(problems)
}

func validDigest(value, prefix string) bool {
	return strings.HasPrefix(value, prefix) && sha256ValuePattern.MatchString(value)
}

func hashCanonical(prefix string, value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return prefix + hex.EncodeToString(digest[:]), nil
}

func SHA256Digest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func sortedUniqueCopy(values []string) []string {
	result := append([]string{}, values...)
	sort.Strings(result)
	if len(result) < 2 {
		return result
	}
	write := 1
	for read := 1; read < len(result); read++ {
		if result[read] == result[write-1] {
			continue
		}
		result[write] = result[read]
		write++
	}
	return result[:write]
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
