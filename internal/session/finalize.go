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
	SessionDir string
}

type Finalized struct {
	SchemaVersion  int    `json:"schema_version"`
	Status         string `json:"status"`
	SessionDir     string `json:"session_dir"`
	RepositoryDir  string `json:"repository_dir,omitempty"`
	DiffPath       string `json:"diff_path,omitempty"`
	RubricPath     string `json:"rubric_path,omitempty"`
	ResultPath     string `json:"result_path,omitempty"`
	MarkdownPath   string `json:"markdown_path,omitempty"`
	SemanticResult string `json:"semantic_result,omitempty"`
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
		return writeIncomplete(layout, "", request, policy, quality.Execution{}, "read session metadata: "+err.Error())
	}
	metadata, err := quality.DecodeStrict[Metadata](bytes.NewReader(metadataRaw))
	if err != nil || metadata.SchemaVersion != 1 || (metadata.Host != "claude-code" && metadata.Host != "codex") || metadata.SkillVersion != quality.SkillVersion {
		return writeIncomplete(layout, "", request, policy, quality.Execution{}, "session metadata is invalid")
	}
	execution := quality.Execution{Host: metadata.Host, SkillVersion: metadata.SkillVersion, AgentCount: 1}
	mainRaw, err := ReadRegularFile(layout.MainReviewPath, maxReviewBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return writeIncomplete(layout, metadata.RepositoryRoot, request, policy, execution, "main review is missing")
		}
		return writeIncomplete(layout, metadata.RepositoryRoot, request, policy, execution, "read main review: "+err.Error())
	}
	main, err := quality.DecodeModelReview(bytes.NewReader(mainRaw))
	if err != nil {
		return writeIncomplete(layout, metadata.RepositoryRoot, request, policy, execution, "invalid main review: "+err.Error())
	}
	main.Execution = execution
	return writeComplete(layout, metadata.RepositoryRoot, request, main, policy)
}

func writeComplete(layout Layout, repositoryRoot string, request quality.ReviewRequest, review quality.ModelReview, policy quality.PolicyManifest) (Finalized, error) {
	result := quality.Adjudicate(request, review, policy)
	if err := writeReports(layout, result); err != nil {
		return Finalized{}, err
	}
	cleanupWorktree(repositoryRoot, layout.RepositoryDir)
	return Finalized{
		SchemaVersion:  1,
		Status:         "COMPLETE",
		SessionDir:     layout.SessionDir,
		ResultPath:     layout.ResultPath,
		MarkdownPath:   layout.MarkdownPath,
		SemanticResult: result.Adjudication.SemanticResult,
	}, nil
}

func writeIncomplete(layout Layout, repositoryRoot string, request quality.ReviewRequest, policy quality.PolicyManifest, execution quality.Execution, reasons ...string) (Finalized, error) {
	result := quality.IncompleteResultWithExecution(request, policy, execution, reasons...)
	if err := writeReports(layout, result); err != nil {
		return Finalized{}, err
	}
	cleanupWorktree(repositoryRoot, layout.RepositoryDir)
	return Finalized{
		SchemaVersion:  1,
		Status:         "INCOMPLETE",
		SessionDir:     layout.SessionDir,
		ResultPath:     layout.ResultPath,
		MarkdownPath:   layout.MarkdownPath,
		SemanticResult: quality.ResultIncomplete,
	}, nil
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
