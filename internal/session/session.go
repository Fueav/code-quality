package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	bundle "github.com/Fueav/code-quality"
	"github.com/Fueav/code-quality/quality"
)

const maxDiffBytes = int64(2 << 20)

type CheckoutMode string

const (
	CheckoutModeWorktree CheckoutMode = "worktree"
	CheckoutModeClone    CheckoutMode = "clone"
)

type Layout struct {
	SessionDir             string
	InputDir               string
	RepositoryDir          string
	EvidenceDir            string
	OutputDir              string
	EvidenceContextPath    string
	RequestPath            string
	DiffPath               string
	RubricPath             string
	WorkflowPath           string
	ModelSchemaPath        string
	NativeReviewSchemaPath string
	VerifierSchemaPath     string
	ManifestPath           string
	MetadataPath           string
	MainReviewPath         string
	NativeReviewPath       string
	VerifierOutputPath     string
	NativeStdoutPath       string
	NativeStderrPath       string
	VerifierStdoutPath     string
	VerifierStderrPath     string
	RereviewPath           string
	ReviewInvalidPath      string
	ResultPath             string
	MarkdownPath           string
}

type Prepared struct {
	SchemaVersion          int          `json:"schema_version"`
	Status                 string       `json:"status"`
	SessionDir             string       `json:"session_dir"`
	RepositoryDir          string       `json:"repository_dir"`
	EvidenceContextPath    string       `json:"evidence_context_path"`
	RequestPath            string       `json:"request_path"`
	DiffPath               string       `json:"diff_path"`
	RubricPath             string       `json:"rubric_path"`
	WorkflowPath           string       `json:"workflow_path"`
	ModelSchemaPath        string       `json:"model_schema_path"`
	NativeReviewSchemaPath string       `json:"native_review_schema_path,omitempty"`
	VerifierSchemaPath     string       `json:"verifier_schema_path,omitempty"`
	ManifestPath           string       `json:"manifest_path"`
	MetadataPath           string       `json:"metadata_path"`
	MainReviewPath         string       `json:"main_review_path"`
	NativeReviewPath       string       `json:"native_review_path,omitempty"`
	VerifierOutputPath     string       `json:"verifier_output_path,omitempty"`
	ResultPath             string       `json:"result_path,omitempty"`
	MarkdownPath           string       `json:"markdown_path,omitempty"`
	DirtyWorktree          bool         `json:"dirty_worktree"`
	CheckoutMode           CheckoutMode `json:"checkout_mode"`
	EvidencePresent        bool         `json:"evidence_present"`
}

type Options struct {
	RepositoryRoot string
	OutputRoot     string
	Host           string
	Request        quality.ReviewRequest
	DirtyWorktree  bool
}

type Metadata struct {
	SchemaVersion  int          `json:"schema_version"`
	Host           string       `json:"host"`
	SkillVersion   string       `json:"skill_version"`
	RepositoryRoot string       `json:"repository_root"`
	CheckoutMode   CheckoutMode `json:"checkout_mode"`
	RuntimeMode    string       `json:"runtime_mode,omitempty"`
}

func Prepare(ctx context.Context, options Options) (Prepared, error) {
	return prepare(ctx, options, false)
}

func PrepareNative(ctx context.Context, options Options) (Prepared, error) {
	if options.Host != "codex" {
		return Prepared{}, errors.New("native review host must be codex")
	}
	return prepare(ctx, options, true)
}

func prepare(ctx context.Context, options Options, native bool) (Prepared, error) {
	if strings.TrimSpace(options.RepositoryRoot) == "" {
		return Prepared{}, errors.New("repository root is required")
	}
	if options.Host != "claude-code" && options.Host != "codex" {
		return Prepared{}, errors.New("host must be claude-code or codex")
	}
	if errors := quality.ValidateRequest(options.Request); len(errors) > 0 {
		return Prepared{}, fmt.Errorf("review request is invalid: %s", strings.Join(errors, "; "))
	}
	root, err := prepareOutputRoot(options.OutputRoot)
	if err != nil {
		return Prepared{}, err
	}
	directory, err := os.MkdirTemp(root, "review-")
	if err != nil {
		return Prepared{}, fmt.Errorf("create review session: %w", err)
	}
	layout := NewLayout(directory)
	prepared := false
	checkoutMode := CheckoutModeWorktree
	defer func() {
		if !prepared {
			_ = cleanupCheckout(options.RepositoryRoot, layout.SessionDir, layout.RepositoryDir, checkoutMode)
			_ = cleanupPartialSession(root, directory)
		}
	}()
	if err := os.MkdirAll(layout.InputDir, 0o700); err != nil {
		return Prepared{}, err
	}
	if err := os.MkdirAll(layout.OutputDir, 0o700); err != nil {
		return Prepared{}, err
	}
	checkoutMode, err = prepareCheckout(ctx, options.RepositoryRoot, options.Request.TargetCommit, layout)
	if err != nil {
		return Prepared{}, err
	}
	evidencePresent := false
	if !native {
		evidence, err := DiscoverEvidence(options.RepositoryRoot, options.Request.TargetCommit, layout.EvidenceDir)
		if err != nil {
			return Prepared{}, fmt.Errorf("discover optional Harness evidence: %w", err)
		}
		if err := encodeEvidenceContext(layout.EvidenceContextPath, evidence); err != nil {
			return Prepared{}, err
		}
		evidencePresent = len(evidence.Sources) > 0 || len(evidence.Rejected) > 0
	}
	if err := writeJSON(layout.RequestPath, options.Request); err != nil {
		return Prepared{}, err
	}
	runtimeMode := "session_agent"
	if native {
		runtimeMode = "codex_native_review"
	}
	if err := writeJSON(layout.MetadataPath, Metadata{SchemaVersion: 1, Host: options.Host, SkillVersion: quality.SkillVersion, RepositoryRoot: options.RepositoryRoot, CheckoutMode: checkoutMode, RuntimeMode: runtimeMode}); err != nil {
		return Prepared{}, err
	}
	if err := writeTrustedDiff(ctx, options.RepositoryRoot, options.Request, layout.DiffPath); err != nil {
		return Prepared{}, err
	}
	if native {
		if err := writeSchema(layout.NativeReviewSchemaPath, "native-review.schema.json"); err != nil {
			return Prepared{}, err
		}
		if err := writeSchema(layout.VerifierSchemaPath, "candidate-verifier.schema.json"); err != nil {
			return Prepared{}, err
		}
	} else {
		if err := writeEmbedded(layout.RubricPath, bundle.ReviewLens); err != nil {
			return Prepared{}, err
		}
		if err := writeEmbedded(layout.WorkflowPath, bundle.Workflow); err != nil {
			return Prepared{}, err
		}
		if err := writeSchema(layout.ModelSchemaPath, "model-review.schema.json"); err != nil {
			return Prepared{}, err
		}
	}
	prepared = true
	result := Prepared{
		SchemaVersion:       1,
		Status:              "READY_FOR_MAIN_REVIEW",
		SessionDir:          layout.SessionDir,
		RepositoryDir:       layout.RepositoryDir,
		EvidenceContextPath: layout.EvidenceContextPath,
		RequestPath:         layout.RequestPath,
		DiffPath:            layout.DiffPath,
		RubricPath:          layout.RubricPath,
		WorkflowPath:        layout.WorkflowPath,
		ModelSchemaPath:     layout.ModelSchemaPath,
		ManifestPath:        layout.ManifestPath,
		MetadataPath:        layout.MetadataPath,
		MainReviewPath:      layout.MainReviewPath,
		ResultPath:          layout.ResultPath,
		MarkdownPath:        layout.MarkdownPath,
		DirtyWorktree:       options.DirtyWorktree,
		CheckoutMode:        checkoutMode,
		EvidencePresent:     evidencePresent,
	}
	if native {
		result.Status = "READY_FOR_NATIVE_REVIEW"
		result.EvidenceContextPath = ""
		result.RubricPath = ""
		result.WorkflowPath = ""
		result.ModelSchemaPath = ""
		result.MainReviewPath = ""
		result.NativeReviewSchemaPath = layout.NativeReviewSchemaPath
		result.VerifierSchemaPath = layout.VerifierSchemaPath
		result.NativeReviewPath = layout.NativeReviewPath
		result.VerifierOutputPath = layout.VerifierOutputPath
	}
	return result, nil
}

func NewLayout(directory string) Layout {
	absolute, err := filepath.Abs(directory)
	if err == nil {
		directory = absolute
	}
	input := filepath.Join(directory, "input")
	output := filepath.Join(directory, "output")
	return Layout{
		SessionDir:             directory,
		InputDir:               input,
		RepositoryDir:          filepath.Join(input, "repository"),
		EvidenceDir:            filepath.Join(input, "evidence"),
		OutputDir:              output,
		EvidenceContextPath:    filepath.Join(input, "evidence-context.json"),
		RequestPath:            filepath.Join(input, "review-request.json"),
		DiffPath:               filepath.Join(input, "trusted.diff"),
		RubricPath:             filepath.Join(input, "rubric.md"),
		WorkflowPath:           filepath.Join(input, "workflow.md"),
		ModelSchemaPath:        filepath.Join(input, "model-review.schema.json"),
		NativeReviewSchemaPath: filepath.Join(input, "native-review.schema.json"),
		VerifierSchemaPath:     filepath.Join(input, "candidate-verifier.schema.json"),
		ManifestPath:           filepath.Join(directory, "input-manifest.json"),
		MetadataPath:           filepath.Join(input, "session-metadata.json"),
		MainReviewPath:         filepath.Join(output, "main-review.json"),
		NativeReviewPath:       filepath.Join(output, "native-review.json"),
		VerifierOutputPath:     filepath.Join(output, "candidate-verdicts.json"),
		NativeStdoutPath:       filepath.Join(output, "native-review.stdout.log"),
		NativeStderrPath:       filepath.Join(output, "native-review.stderr.log"),
		VerifierStdoutPath:     filepath.Join(output, "candidate-verifier.stdout.log"),
		VerifierStderrPath:     filepath.Join(output, "candidate-verifier.stderr.log"),
		RereviewPath:           filepath.Join(output, "rereview.json"),
		ReviewInvalidPath:      filepath.Join(output, ".review-invalid-attempted"),
		ResultPath:             filepath.Join(output, "review-result.json"),
		MarkdownPath:           filepath.Join(output, "review-result.md"),
	}
}

// CleanupPreparedCheckout removes only the isolated checkout created for a
// prepared session. The session inputs, raw model outputs, and final reports
// remain available.
func CleanupPreparedCheckout(repositoryRoot string, prepared Prepared) error {
	layout := NewLayout(prepared.SessionDir)
	if filepath.Clean(prepared.RepositoryDir) != layout.RepositoryDir {
		return errors.New("prepared repository path does not match its session")
	}
	switch prepared.CheckoutMode {
	case CheckoutModeWorktree:
		var stderr bytes.Buffer
		command := exec.Command("git", "-C", repositoryRoot, "worktree", "remove", "--force", layout.RepositoryDir)
		command.Stderr = &stderr
		if err := command.Run(); err != nil {
			return fmt.Errorf("remove review worktree: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil
	case CheckoutModeClone:
		return removeCloneCheckout(layout.SessionDir, layout.RepositoryDir)
	default:
		return fmt.Errorf("unsupported checkout mode %q", prepared.CheckoutMode)
	}
}

func ValidateLayout(layout Layout) error {
	for name, path := range map[string]string{
		"session":    layout.SessionDir,
		"input":      layout.InputDir,
		"repository": layout.RepositoryDir,
		"output":     layout.OutputDir,
	} {
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("read %s directory: %w", name, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s must be a non-symlink directory", name)
		}
	}
	return nil
}

func ReadRegularFile(path string, limit int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("input must be a regular non-symlink file")
	}
	if before.Size() > limit {
		return nil, fmt.Errorf("input exceeds %d bytes", limit)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() > limit {
		return nil, errors.New("input changed while it was being read")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != after.Size() || int64(len(raw)) > limit {
		return nil, errors.New("input changed while it was being read")
	}
	return raw, nil
}

func prepareOutputRoot(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("output root is required")
	}
	root, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return "", err
		}
		return root, nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("output root must be a non-symlink directory")
	}
	return root, nil
}

func writeEmbedded(path string, load func() ([]byte, error)) error {
	contents, err := load()
	if err != nil {
		return err
	}
	return os.WriteFile(path, contents, 0o400)
}

func writeSchema(path, name string) error {
	return writeEmbedded(path, func() ([]byte, error) { return bundle.Schema(name) })
}

func writeJSON(path string, value any) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o400)
	if err != nil {
		return err
	}
	if err := quality.EncodeJSON(file, value); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func writeTrustedDiff(ctx context.Context, root string, request quality.ReviewRequest, path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o400)
	if err != nil {
		return err
	}
	err = writeGitOutputLimited(ctx, root, file, maxDiffBytes,
		"diff", "--no-ext-diff", "--unified=6", request.BaseCommit, request.TargetCommit, "--",
	)
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("build trusted diff: %w", err)
	}
	return closeErr
}

func writeGitOutputLimited(ctx context.Context, root string, writer io.Writer, limit int64, arguments ...string) error {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, arguments...)...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return err
	}
	written, copyErr := io.Copy(writer, io.LimitReader(stdout, limit+1))
	if copyErr != nil || written > limit {
		_ = stdout.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
		if copyErr != nil {
			return copyErr
		}
		return fmt.Errorf("Git output exceeds %d bytes", limit)
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func addWorktree(ctx context.Context, root, commit, worktreePath string) error {
	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, "git", "-C", root, "worktree", "add", "--detach", worktreePath, commit)
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("add review worktree: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func prepareCheckout(ctx context.Context, root, commit string, layout Layout) (CheckoutMode, error) {
	if err := addWorktree(ctx, root, commit, layout.RepositoryDir); err == nil {
		return CheckoutModeWorktree, nil
	} else {
		worktreeErr := err
		removeWorktree(root, layout.RepositoryDir)
		if err := removeCloneCheckout(layout.SessionDir, layout.RepositoryDir); err != nil {
			return CheckoutModeClone, fmt.Errorf("%v; prepare shared-clone fallback: %w", worktreeErr, err)
		}
		if err := addSharedClone(ctx, root, commit, layout.RepositoryDir); err != nil {
			return CheckoutModeClone, fmt.Errorf("%v; prepare shared-clone fallback: %w", worktreeErr, err)
		}
		return CheckoutModeClone, nil
	}
}

func addSharedClone(ctx context.Context, root, commit, clonePath string) error {
	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, "git", "clone", "--shared", "--no-checkout", root, clonePath)
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("clone shared review repository: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	stderr.Reset()
	command = exec.CommandContext(ctx, "git", "-C", clonePath, "checkout", "--detach", commit)
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("checkout shared review repository: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// removeWorktree is best-effort cleanup; a leftover worktree can be pruned with
// `git worktree prune` and does not affect the review result.
func removeWorktree(root, worktreePath string) {
	_ = exec.Command("git", "-C", root, "worktree", "remove", "--force", worktreePath).Run()
}

func cleanupCheckout(root, sessionDir, repositoryDir string, mode CheckoutMode) error {
	switch mode {
	case CheckoutModeWorktree:
		removeWorktree(root, repositoryDir)
		return nil
	case CheckoutModeClone:
		return removeCloneCheckout(sessionDir, repositoryDir)
	default:
		return fmt.Errorf("unsupported checkout mode %q", mode)
	}
}

func removeCloneCheckout(sessionDir, repositoryDir string) error {
	sessionAbsolute, err := filepath.Abs(sessionDir)
	if err != nil {
		return err
	}
	repositoryAbsolute, err := filepath.Abs(repositoryDir)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(sessionAbsolute, repositoryAbsolute)
	if err != nil {
		return err
	}
	if relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("refuse to clean clone checkout outside session")
	}
	return os.RemoveAll(repositoryAbsolute)
}

func cleanupPartialSession(root, directory string) error {
	if filepath.Dir(directory) != root || !strings.HasPrefix(filepath.Base(directory), "review-") {
		return errors.New("refuse to clean unsafe session path")
	}
	return os.RemoveAll(directory)
}
