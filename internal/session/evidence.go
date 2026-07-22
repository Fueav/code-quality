package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Fueav/code-quality/quality"
)

const (
	maxEvidenceFileBytes  = int64(2 << 20)
	maxEvidenceTotalBytes = int64(20 << 20)
	maxEvidenceFiles      = 256
)

type EvidenceContext struct {
	SchemaVersion int                `json:"schema_version"`
	Sources       []EvidenceSource   `json:"sources"`
	Rejected      []RejectedEvidence `json:"rejected"`
}

type EvidenceSource struct {
	Mode       string   `json:"mode"`
	SourcePath string   `json:"source_path"`
	HeadSHA    string   `json:"head_sha"`
	Files      []string `json:"files"`
}

type RejectedEvidence struct {
	Mode   string `json:"mode"`
	Reason string `json:"reason"`
}

type evidenceSummary struct {
	SchemaVersion          int              `json:"schema_version"`
	Mode                   string           `json:"mode"`
	Overall                string           `json:"overall"`
	ArtifactManifestSHA256 string           `json:"artifact_manifest_sha256"`
	Artifacts              []artifactRecord `json:"artifacts"`
	Git                    struct {
		HeadSHA string `json:"head_sha"`
	} `json:"git"`
}

type artifactManifest struct {
	SchemaVersion int              `json:"schema_version"`
	Artifacts     []artifactRecord `json:"artifacts"`
}

type artifactRecord struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

type validatedEvidence struct {
	Source   EvidenceSource
	Contents map[string][]byte
}

func DiscoverEvidence(repositoryRoot, targetCommit, destination string) (EvidenceContext, error) {
	context := EvidenceContext{SchemaVersion: 1, Sources: []EvidenceSource{}, Rejected: []RejectedEvidence{}}
	artifactsRoot := filepath.Join(repositoryRoot, ".artifacts")
	artifactsInfo, err := os.Lstat(artifactsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return context, nil
	}
	if err != nil || !artifactsInfo.IsDir() || artifactsInfo.Mode()&os.ModeSymlink != 0 {
		reason := "evidence parent must be a non-symlink directory"
		if err != nil {
			reason = err.Error()
		}
		for _, mode := range []string{"change", "candidate", "release"} {
			context.Rejected = append(context.Rejected, RejectedEvidence{Mode: mode, Reason: reason})
		}
		return context, nil
	}
	for _, mode := range []string{"change", "candidate", "release"} {
		source := filepath.Join(artifactsRoot, mode)
		info, err := os.Lstat(source)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			reason := "evidence root must be a non-symlink directory"
			if err != nil {
				reason = err.Error()
			}
			context.Rejected = append(context.Rejected, RejectedEvidence{Mode: mode, Reason: reason})
			continue
		}
		summaryPath := filepath.Join(source, "summary.json")
		if _, err := os.Lstat(summaryPath); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			context.Rejected = append(context.Rejected, RejectedEvidence{Mode: mode, Reason: err.Error()})
			continue
		}
		evidence, err := validateEvidenceSource(source, mode, targetCommit)
		if err != nil {
			context.Rejected = append(context.Rejected, RejectedEvidence{Mode: mode, Reason: err.Error()})
			continue
		}
		target := filepath.Join(destination, mode)
		if err := copyEvidenceSource(target, evidence.Contents); err != nil {
			return EvidenceContext{}, err
		}
		evidence.Source.SourcePath = filepath.ToSlash(filepath.Join("evidence", mode))
		context.Sources = append(context.Sources, evidence.Source)
	}
	return context, nil
}

func validateEvidenceSource(source, mode, targetCommit string) (validatedEvidence, error) {
	info, err := os.Lstat(source)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return validatedEvidence{}, errors.New("evidence root must be a non-symlink directory")
	}
	summaryRaw, err := readEvidenceRelative(source, "summary.json")
	if err != nil {
		return validatedEvidence{}, fmt.Errorf("summary: %w", err)
	}
	var summary evidenceSummary
	if err := json.Unmarshal(summaryRaw, &summary); err != nil {
		return validatedEvidence{}, fmt.Errorf("summary: %w", err)
	}
	if summary.SchemaVersion != 1 || summary.Mode != mode || summary.Overall != "passed" {
		return validatedEvidence{}, errors.New("summary is not passed evidence for this mode")
	}
	if summary.Git.HeadSHA != targetCommit {
		return validatedEvidence{}, errors.New("summary head_sha does not match target_commit")
	}
	manifestRaw, err := readEvidenceRelative(source, "artifact_manifest.json")
	if err != nil {
		return validatedEvidence{}, fmt.Errorf("artifact manifest: %w", err)
	}
	manifestDigest := sha256.Sum256(manifestRaw)
	if hex.EncodeToString(manifestDigest[:]) != summary.ArtifactManifestSHA256 {
		return validatedEvidence{}, errors.New("artifact manifest digest does not match summary")
	}
	manifest, err := quality.DecodeStrict[artifactManifest](bytes.NewReader(manifestRaw))
	if err != nil {
		return validatedEvidence{}, fmt.Errorf("artifact manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 || len(manifest.Artifacts) > maxEvidenceFiles {
		return validatedEvidence{}, errors.New("artifact manifest is outside V1 limits")
	}
	if !sameArtifactRecords(summary.Artifacts, manifest.Artifacts) {
		return validatedEvidence{}, errors.New("summary and artifact manifest disagree")
	}
	files := []string{"artifact_manifest.json", "summary.json"}
	contents := map[string][]byte{"artifact_manifest.json": manifestRaw, "summary.json": summaryRaw}
	seen := map[string]struct{}{}
	var total int64 = int64(len(summaryRaw) + len(manifestRaw))
	for _, record := range manifest.Artifacts {
		if !cleanEvidencePath(record.Path) || record.Path == "summary.json" || record.Path == "artifact_manifest.json" || record.SizeBytes < 0 || record.SizeBytes > maxEvidenceFileBytes || !validSHA256(record.SHA256) {
			return validatedEvidence{}, errors.New("artifact manifest contains an invalid record")
		}
		if _, exists := seen[record.Path]; exists {
			return validatedEvidence{}, errors.New("artifact manifest contains duplicate paths")
		}
		seen[record.Path] = struct{}{}
		raw, err := readEvidenceRelative(source, record.Path)
		if err != nil {
			return validatedEvidence{}, fmt.Errorf("artifact %s: %w", record.Path, err)
		}
		digest := sha256.Sum256(raw)
		if int64(len(raw)) != record.SizeBytes || hex.EncodeToString(digest[:]) != record.SHA256 {
			return validatedEvidence{}, fmt.Errorf("artifact %s does not match its manifest", record.Path)
		}
		total += int64(len(raw))
		if total > maxEvidenceTotalBytes {
			return validatedEvidence{}, errors.New("evidence exceeds the V1 total size limit")
		}
		files = append(files, record.Path)
		contents[record.Path] = raw
	}
	sort.Strings(files)
	return validatedEvidence{
		Source:   EvidenceSource{Mode: mode, HeadSHA: targetCommit, Files: files},
		Contents: contents,
	}, nil
}

func sameArtifactRecords(left, right []artifactRecord) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
		if index > 0 && left[index-1].Path >= left[index].Path {
			return false
		}
	}
	return true
}

func copyEvidenceSource(destination string, contents map[string][]byte) error {
	files := make([]string, 0, len(contents))
	for relative := range contents {
		files = append(files, relative)
	}
	sort.Strings(files)
	for _, relative := range files {
		target := filepath.Join(destination, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(target, contents[relative], 0o600); err != nil {
			return err
		}
	}
	return nil
}

func readEvidenceRelative(root, relative string) ([]byte, error) {
	if !cleanEvidencePath(relative) {
		return nil, errors.New("evidence path is invalid")
	}
	current := root
	parts := strings.Split(filepath.ToSlash(relative), "/")
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("evidence path contains a non-directory or symlink")
		}
	}
	return readEvidenceFile(filepath.Join(root, filepath.FromSlash(relative)))
}

func readEvidenceFile(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("evidence must be a regular non-symlink file")
	}
	if before.Size() > maxEvidenceFileBytes {
		return nil, errors.New("evidence file exceeds the V1 size limit")
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
	if !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() > maxEvidenceFileBytes {
		return nil, errors.New("evidence changed while it was being read")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxEvidenceFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != after.Size() || int64(len(raw)) > maxEvidenceFileBytes {
		return nil, errors.New("evidence changed while it was being read")
	}
	return raw, nil
}

func cleanEvidencePath(value string) bool {
	if value == "" || value == "." || strings.Contains(value, `\`) || filepath.VolumeName(value) != "" || filepath.IsAbs(value) {
		return false
	}
	native := filepath.FromSlash(value)
	clean := filepath.Clean(native)
	return clean == native && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func encodeEvidenceContext(path string, context EvidenceContext) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(context); err != nil {
		return err
	}
	return os.WriteFile(path, buffer.Bytes(), 0o400)
}
