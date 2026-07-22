package session

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

const (
	maxSnapshotBytes = int64(512 << 20)
	maxDiffBytes     = int64(2 << 20)
)

type Layout struct {
	SessionDir          string
	InputDir            string
	RepositoryDir       string
	EvidenceDir         string
	OutputDir           string
	EvidenceContextPath string
	RequestPath         string
	DiffPath            string
	RubricPath          string
	WorkflowPath        string
	ModelSchemaPath     string
	VerifierSchemaPath  string
	ManifestPath        string
	MetadataPath        string
	MainReviewPath      string
	VerifierRequestPath string
	VerifierReviewPath  string
	ResultPath          string
	MarkdownPath        string
}

type Prepared struct {
	SchemaVersion       int    `json:"schema_version"`
	Status              string `json:"status"`
	SessionDir          string `json:"session_dir"`
	RepositoryDir       string `json:"repository_dir"`
	EvidenceContextPath string `json:"evidence_context_path"`
	RequestPath         string `json:"request_path"`
	DiffPath            string `json:"diff_path"`
	RubricPath          string `json:"rubric_path"`
	WorkflowPath        string `json:"workflow_path"`
	ModelSchemaPath     string `json:"model_schema_path"`
	VerifierSchemaPath  string `json:"verifier_schema_path"`
	ManifestPath        string `json:"manifest_path"`
	MetadataPath        string `json:"metadata_path"`
	MainReviewPath      string `json:"main_review_path"`
	DirtyWorktree       bool   `json:"dirty_worktree"`
}

type Options struct {
	RepositoryRoot string
	OutputRoot     string
	Host           string
	Request        quality.ReviewRequest
	DirtyWorktree  bool
}

type Metadata struct {
	SchemaVersion int    `json:"schema_version"`
	Host          string `json:"host"`
	SkillVersion  string `json:"skill_version"`
}

func Prepare(ctx context.Context, options Options) (Prepared, error) {
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
	prepared := false
	defer func() {
		if !prepared {
			_ = cleanupPartialSession(root, directory)
		}
	}()
	layout := NewLayout(directory)
	if err := os.MkdirAll(layout.RepositoryDir, 0o700); err != nil {
		return Prepared{}, err
	}
	if err := os.MkdirAll(layout.OutputDir, 0o700); err != nil {
		return Prepared{}, err
	}
	if err := extractCommit(ctx, options.RepositoryRoot, options.Request.TargetCommit, layout.RepositoryDir); err != nil {
		return Prepared{}, err
	}
	evidence, err := DiscoverEvidence(options.RepositoryRoot, options.Request.TargetCommit, layout.EvidenceDir)
	if err != nil {
		return Prepared{}, fmt.Errorf("discover optional Harness evidence: %w", err)
	}
	if err := encodeEvidenceContext(layout.EvidenceContextPath, evidence); err != nil {
		return Prepared{}, err
	}
	if err := writeJSON(layout.RequestPath, options.Request); err != nil {
		return Prepared{}, err
	}
	if err := writeJSON(layout.MetadataPath, Metadata{SchemaVersion: 1, Host: options.Host, SkillVersion: quality.SkillVersion}); err != nil {
		return Prepared{}, err
	}
	if err := writeTrustedDiff(ctx, options.RepositoryRoot, options.Request, layout.DiffPath); err != nil {
		return Prepared{}, err
	}
	if err := writeEmbedded(layout.RubricPath, bundle.Rubric); err != nil {
		return Prepared{}, err
	}
	if err := writeEmbedded(layout.WorkflowPath, bundle.Workflow); err != nil {
		return Prepared{}, err
	}
	if err := writeSchema(layout.ModelSchemaPath, "model-review.schema.json"); err != nil {
		return Prepared{}, err
	}
	if err := writeSchema(layout.VerifierSchemaPath, "verifier-review.schema.json"); err != nil {
		return Prepared{}, err
	}
	if err := writeInputManifest(layout); err != nil {
		return Prepared{}, err
	}
	if err := makeReadOnly(layout.InputDir); err != nil {
		return Prepared{}, fmt.Errorf("make review input read-only: %w", err)
	}
	prepared = true
	return Prepared{
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
		VerifierSchemaPath:  layout.VerifierSchemaPath,
		ManifestPath:        layout.ManifestPath,
		MetadataPath:        layout.MetadataPath,
		MainReviewPath:      layout.MainReviewPath,
		DirtyWorktree:       options.DirtyWorktree,
	}, nil
}

func NewLayout(directory string) Layout {
	absolute, err := filepath.Abs(directory)
	if err == nil {
		directory = absolute
	}
	input := filepath.Join(directory, "input")
	output := filepath.Join(directory, "output")
	return Layout{
		SessionDir:          directory,
		InputDir:            input,
		RepositoryDir:       filepath.Join(input, "repository"),
		EvidenceDir:         filepath.Join(input, "evidence"),
		OutputDir:           output,
		EvidenceContextPath: filepath.Join(input, "evidence-context.json"),
		RequestPath:         filepath.Join(input, "review-request.json"),
		DiffPath:            filepath.Join(input, "trusted.diff"),
		RubricPath:          filepath.Join(input, "rubric.md"),
		WorkflowPath:        filepath.Join(input, "workflow.md"),
		ModelSchemaPath:     filepath.Join(input, "model-review.schema.json"),
		VerifierSchemaPath:  filepath.Join(input, "verifier-review.schema.json"),
		ManifestPath:        filepath.Join(directory, "input-manifest.json"),
		MetadataPath:        filepath.Join(input, "session-metadata.json"),
		MainReviewPath:      filepath.Join(output, "main-review.json"),
		VerifierRequestPath: filepath.Join(output, "verifier-request.json"),
		VerifierReviewPath:  filepath.Join(output, "verifier-review.json"),
		ResultPath:          filepath.Join(output, "review-result.json"),
		MarkdownPath:        filepath.Join(output, "review-result.md"),
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

type inputManifest struct {
	SchemaVersion int               `json:"schema_version"`
	Files         map[string]string `json:"files"`
}

func writeInputManifest(layout Layout) error {
	files, err := inputDigests(layout.InputDir)
	if err != nil {
		return err
	}
	return writeJSON(layout.ManifestPath, inputManifest{SchemaVersion: 1, Files: files})
}

func VerifyInputManifest(layout Layout) error {
	raw, err := ReadRegularFile(layout.ManifestPath, maxReviewBytes)
	if err != nil {
		return fmt.Errorf("read input manifest: %w", err)
	}
	manifest, err := quality.DecodeStrict[inputManifest](bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("decode input manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 {
		return errors.New("input manifest schema_version must be 1")
	}
	actual, err := inputDigests(layout.InputDir)
	if err != nil {
		return err
	}
	if len(actual) != len(manifest.Files) {
		return errors.New("trusted review input was modified")
	}
	for path, digest := range manifest.Files {
		actualDigest, exists := actual[path]
		if !exists || !validSHA256(digest) || actualDigest != digest {
			return errors.New("trusted review input was modified")
		}
	}
	return nil
}

func inputDigests(root string) (map[string]string, error) {
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("trusted input contains unsupported path %s", path)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(contents)
		result[filepath.ToSlash(relative)] = hex.EncodeToString(digest[:])
		return nil
	})
	return result, err
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
		"diff", "--no-ext-diff", "--unified=40", request.BaseCommit, request.TargetCommit, "--",
	)
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("build trusted diff: %w", err)
	}
	return closeErr
}

func extractCommit(ctx context.Context, root, commit, destination string) error {
	command := exec.CommandContext(ctx, "git", "-C", root, "archive", "--format=tar", commit)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start target archive: %w", err)
	}
	extractErr := extractTar(stdout, destination, maxSnapshotBytes)
	if extractErr != nil {
		_ = stdout.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
		return extractErr
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("archive target commit: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func extractTar(reader io.Reader, destination string, maxBytes int64) error {
	archive := tar.NewReader(reader)
	var total int64
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read target archive: %w", err)
		}
		clean := filepath.Clean(header.Name)
		if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive contains unsafe path %q", header.Name)
		}
		target := filepath.Join(destination, clean)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := writeArchiveFile(archive, target, header.Size, &total, maxBytes); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := writeArchiveBytes(target, []byte(header.Linkname), &total, maxBytes); err != nil {
				return err
			}
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			continue
		default:
			return fmt.Errorf("archive contains unsupported entry %q", header.Name)
		}
	}
}

func writeArchiveFile(reader io.Reader, target string, size int64, total *int64, limit int64) error {
	if size < 0 || *total+size > limit {
		return errors.New("target snapshot exceeds the configured limit")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyN(file, reader, size)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	*total += size
	return nil
}

func writeArchiveBytes(target string, contents []byte, total *int64, limit int64) error {
	if *total+int64(len(contents)) > limit {
		return errors.New("target snapshot exceeds the configured limit")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(target, contents, 0o600); err != nil {
		return err
	}
	*total += int64(len(contents))
	return nil
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

func makeReadOnly(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o500)
		}
		return os.Chmod(path, 0o400)
	})
}

func cleanupPartialSession(root, directory string) error {
	if filepath.Dir(directory) != root || !strings.HasPrefix(filepath.Base(directory), "review-") {
		return errors.New("refuse to clean unsafe session path")
	}
	return os.RemoveAll(directory)
}
