package session

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Fueav/code-quality/quality"
)

const maxReviewBytes = int64(10 << 20)

type FinalizeOptions struct {
	SessionDir          string
	VerifierUnavailable string
}

type Finalized struct {
	SchemaVersion       int    `json:"schema_version"`
	Status              string `json:"status"`
	SessionDir          string `json:"session_dir"`
	VerifierRequestPath string `json:"verifier_request_path,omitempty"`
	VerifierReviewPath  string `json:"verifier_review_path,omitempty"`
	RepositoryDir       string `json:"repository_dir,omitempty"`
	DiffPath            string `json:"diff_path,omitempty"`
	RubricPath          string `json:"rubric_path,omitempty"`
	VerifierSchemaPath  string `json:"verifier_schema_path,omitempty"`
	ResultPath          string `json:"result_path,omitempty"`
	MarkdownPath        string `json:"markdown_path,omitempty"`
	SemanticResult      string `json:"semantic_result,omitempty"`
}

func Finalize(options FinalizeOptions, policy quality.PolicyManifest) (Finalized, error) {
	layout := NewLayout(options.SessionDir)
	if err := ValidateLayout(layout); err != nil {
		return Finalized{}, err
	}
	if err := VerifyInputManifest(layout); err != nil {
		return writeIncomplete(layout, quality.ReviewRequest{}, policy, quality.Execution{}, err.Error())
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
		return writeIncomplete(layout, request, policy, quality.Execution{}, "read session metadata: "+err.Error())
	}
	metadata, err := quality.DecodeStrict[Metadata](bytes.NewReader(metadataRaw))
	if err != nil || metadata.SchemaVersion != 1 || (metadata.Host != "claude-code" && metadata.Host != "codex") || metadata.SkillVersion != quality.SkillVersion {
		return writeIncomplete(layout, request, policy, quality.Execution{}, "session metadata is invalid")
	}
	mainExecution := quality.Execution{Host: metadata.Host, SkillVersion: metadata.SkillVersion, AgentCount: 1}
	mainRaw, err := ReadRegularFile(layout.MainReviewPath, maxReviewBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return writeIncomplete(layout, request, policy, mainExecution, "main review is missing")
		}
		return writeIncomplete(layout, request, policy, mainExecution, "read main review: "+err.Error())
	}
	main, err := quality.DecodeModelReview(bytes.NewReader(mainRaw))
	if err != nil {
		return writeIncomplete(layout, request, policy, mainExecution, "invalid main review: "+err.Error())
	}
	main.Execution = quality.Execution{Host: metadata.Host, SkillVersion: metadata.SkillVersion, AgentCount: 1}
	if validationErrors := quality.ValidateMainReview(main, policy); len(validationErrors) > 0 {
		return writeIncomplete(layout, request, policy, mainExecution, "invalid main review: "+strings.Join(validationErrors, "; "))
	}

	candidates := quality.PotentialBlockFindings(main)
	if len(candidates) == 0 {
		if _, err := os.Lstat(layout.VerifierReviewPath); err == nil {
			return writeIncomplete(layout, request, policy, mainExecution, "verifier review is not allowed without a potential BLOCK candidate")
		} else if !errors.Is(err, os.ErrNotExist) {
			return Finalized{}, err
		}
		return writeComplete(layout, request, main, policy)
	}

	if strings.TrimSpace(options.VerifierUnavailable) != "" {
		main.MissingContext = append(main.MissingContext, "Verifier unavailable: "+strings.TrimSpace(options.VerifierUnavailable))
		return writeComplete(layout, request, main, policy)
	}
	if err := ensureVerifierRequest(layout, request, main); err != nil {
		return writeIncomplete(layout, request, policy, mainExecution, "invalid verifier request: "+err.Error())
	}
	verifierRaw, err := ReadRegularFile(layout.VerifierReviewPath, maxReviewBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Finalized{
				SchemaVersion:       1,
				Status:              "NEEDS_VERIFIER",
				SessionDir:          layout.SessionDir,
				VerifierRequestPath: layout.VerifierRequestPath,
				VerifierReviewPath:  layout.VerifierReviewPath,
				RepositoryDir:       layout.RepositoryDir,
				DiffPath:            layout.DiffPath,
				RubricPath:          layout.RubricPath,
				VerifierSchemaPath:  layout.VerifierSchemaPath,
			}, nil
		}
		return Finalized{}, fmt.Errorf("read verifier review: %w", err)
	}
	verifierExecution := quality.Execution{Host: metadata.Host, SkillVersion: metadata.SkillVersion, AgentCount: 2, VerifierCount: 1}
	verifier, err := quality.DecodeVerifierReview(bytes.NewReader(verifierRaw))
	if err != nil {
		return writeIncomplete(layout, request, policy, verifierExecution, "invalid verifier review: "+err.Error())
	}
	if validationErrors := quality.ValidateVerifierReview(verifier, candidates); len(validationErrors) > 0 {
		return writeIncomplete(layout, request, policy, verifierExecution, "invalid verifier review: "+strings.Join(validationErrors, "; "))
	}
	main = quality.MergeVerifierReview(main, verifier)
	main.Execution = verifierExecution
	return writeComplete(layout, request, main, policy)
}

func ensureVerifierRequest(layout Layout, request quality.ReviewRequest, main quality.ModelReview) error {
	value := quality.BuildVerifierRequest(request, main)
	if raw, err := ReadRegularFile(layout.VerifierRequestPath, maxReviewBytes); err == nil {
		existing, err := quality.DecodeStrict[quality.VerifierRequest](bytes.NewReader(raw))
		if err != nil || !equalVerifierRequest(existing, value) {
			return errors.New("existing verifier request does not match the main review")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeJSON(layout.VerifierRequestPath, value)
}

func equalVerifierRequest(left, right quality.VerifierRequest) bool {
	leftJSON, leftErr := marshalCanonical(left)
	rightJSON, rightErr := marshalCanonical(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func marshalCanonical(value any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := quality.EncodeJSON(&buffer, value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func writeComplete(layout Layout, request quality.ReviewRequest, review quality.ModelReview, policy quality.PolicyManifest) (Finalized, error) {
	result := quality.Adjudicate(request, review, policy)
	if err := writeReports(layout, result); err != nil {
		return Finalized{}, err
	}
	return Finalized{
		SchemaVersion:  1,
		Status:         "COMPLETE",
		SessionDir:     layout.SessionDir,
		ResultPath:     layout.ResultPath,
		MarkdownPath:   layout.MarkdownPath,
		SemanticResult: result.Adjudication.SemanticResult,
	}, nil
}

func writeIncomplete(layout Layout, request quality.ReviewRequest, policy quality.PolicyManifest, execution quality.Execution, reasons ...string) (Finalized, error) {
	result := quality.IncompleteResultWithExecution(request, policy, execution, reasons...)
	if err := writeReports(layout, result); err != nil {
		return Finalized{}, err
	}
	return Finalized{
		SchemaVersion:  1,
		Status:         "INCOMPLETE",
		SessionDir:     layout.SessionDir,
		ResultPath:     layout.ResultPath,
		MarkdownPath:   layout.MarkdownPath,
		SemanticResult: quality.ResultIncomplete,
	}, nil
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
