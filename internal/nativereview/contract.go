package nativereview

import (
	"errors"
	"fmt"
	"strings"

	bundle "github.com/Fueav/code-quality"
	"github.com/Fueav/code-quality/quality"
)

const NativePromptContractVersion = "2"

type ContractBundle struct {
	Contract         quality.NativeReviewContract
	OutputSchemaName string
	OutputSchema     []byte
}

func ProviderForHost(host string) (Provider, error) {
	switch strings.TrimSpace(host) {
	case "codex":
		return NewCodexProvider(""), nil
	case "claude", "claude-code":
		return NewClaudeProvider(""), nil
	default:
		return nil, fmt.Errorf("unsupported native review host %q", host)
	}
}

func ResolveContract(provider Provider, model, reasoningEffort, executionProfile, reviewScope string) (ContractBundle, error) {
	if err := validateProvider(provider); err != nil {
		return ContractBundle{}, err
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = provider.defaultModel()
	}
	reasoningEffort = strings.TrimSpace(reasoningEffort)
	if reasoningEffort == "" {
		reasoningEffort = provider.defaultReasoningEffort()
	}
	if err := provider.validateReasoningEffort(reasoningEffort); err != nil {
		return ContractBundle{}, err
	}
	executionProfile = strings.TrimSpace(executionProfile)
	if executionProfile == "" {
		executionProfile = quality.ExecutionProfilePersonal
	}
	if executionProfile != quality.ExecutionProfilePersonal && executionProfile != quality.ExecutionProfileProductionCI {
		return ContractBundle{}, fmt.Errorf("unsupported execution profile %q", executionProfile)
	}
	reviewScope = strings.ToUpper(strings.TrimSpace(reviewScope))
	if reviewScope == "" {
		reviewScope = quality.ReviewScopeFull
	}
	schemaName := "native-review-output.schema.json"
	switch reviewScope {
	case quality.ReviewScopeFull:
	case quality.ReviewScopeIncremental:
		schemaName = "native-review-incremental-output.schema.json"
	default:
		return ContractBundle{}, errors.New("review scope must be full or incremental")
	}
	schema, err := bundle.Schema(schemaName)
	if err != nil {
		return ContractBundle{}, fmt.Errorf("load native review output schema: %w", err)
	}
	return ContractBundle{
		Contract: quality.NativeReviewContract{
			ToolVersion: quality.SkillVersion, ResultSchemaVersion: quality.NativeResultSchemaVersion,
			ProviderOutputSchema: quality.SHA256Digest(schema), PromptContractVersion: NativePromptContractVersion,
			EvaluationRubricVersion: quality.EvaluationRubricVersion, ProviderHost: provider.Host(),
			Model: model, ReasoningEffort: reasoningEffort, ExecutionProfile: executionProfile,
		},
		OutputSchemaName: schemaName,
		OutputSchema:     append([]byte(nil), schema...),
	}, nil
}
