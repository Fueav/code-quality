package codexreview

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

type FrozenArtifact struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Present bool   `json:"present"`
	Bytes   int64  `json:"bytes"`
	SHA256  string `json:"sha256,omitempty"`
}

type NativeFreezeManifest struct {
	SchemaVersion int              `json:"schema_version"`
	Artifacts     []FrozenArtifact `json:"artifacts"`
}

type frozenNativeArtifacts struct {
	FinalMessage []byte
	Manifest     NativeFreezeManifest
	InputTokens  *int64
	OutputTokens *int64
	UsageError   error
}

type rawArtifactSpec struct {
	name     string
	path     string
	capture  bool
	maxBytes int64
}

type lockedRawArtifact struct {
	path          string
	file          *os.File
	expectedBytes int64
	expectedSHA   string
}

func freezeNativeArtifacts(layout reviewsession.Layout) (frozenNativeArtifacts, error) {
	frozen := frozenNativeArtifacts{Manifest: NativeFreezeManifest{SchemaVersion: 1, Artifacts: []FrozenArtifact{}}}
	specs := []rawArtifactSpec{
		{name: "final_message", path: layout.NativeReviewPath, capture: true, maxBytes: maxNativeOutputBytes},
		{name: "jsonl_stdout", path: layout.NativeStdoutPath},
		{name: "stderr", path: layout.NativeStderrPath},
	}
	locked := make([]lockedRawArtifact, 0, len(specs))
	defer func() {
		for _, artifact := range locked {
			_ = artifact.file.Close()
		}
	}()
	for _, spec := range specs {
		entry := FrozenArtifact{Name: spec.name, Path: filepath.Base(spec.path)}
		file, raw, bytesRead, digest, err := snapshotRawArtifact(spec.path, spec.capture, spec.maxBytes)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return frozenNativeArtifacts{}, fmt.Errorf("snapshot raw %s: %w", spec.name, err)
			}
			frozen.Manifest.Artifacts = append(frozen.Manifest.Artifacts, entry)
			continue
		}
		entry.Bytes = bytesRead
		entry.SHA256 = digest
		entry.Present = true
		frozen.Manifest.Artifacts = append(frozen.Manifest.Artifacts, entry)
		locked = append(locked, lockedRawArtifact{
			path: spec.path, file: file, expectedBytes: bytesRead, expectedSHA: digest,
		})
		if spec.capture {
			frozen.FinalMessage = raw
		}
	}
	validateLockedPaths := func() error {
		for _, artifact := range locked {
			if err := validateLockedArtifact(
				artifact.path, artifact.file, artifact.expectedBytes, artifact.expectedSHA,
			); err != nil {
				return err
			}
		}
		for _, artifact := range locked {
			if err := validateLockedArtifactPath(artifact.path, artifact.file, artifact.expectedBytes); err != nil {
				return err
			}
		}
		return nil
	}
	if err := writeDurableFreezeManifest(layout.NativeFreezePath, frozen.Manifest, validateLockedPaths); err != nil {
		return frozenNativeArtifacts{}, fmt.Errorf("write raw freeze manifest: %w", err)
	}
	frozen.UsageError = os.ErrNotExist
	for _, artifact := range locked {
		if artifact.path != layout.NativeStdoutPath {
			continue
		}
		frozen.InputTokens, frozen.OutputTokens, frozen.UsageError = readCodexUsageFile(artifact.file)
		break
	}
	return frozen, nil
}

func snapshotRawArtifact(path string, capture bool, maxBytes int64) (*os.File, []byte, int64, string, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, 0, "", err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, nil, 0, "", errors.New("input must be a regular non-symlink file")
	}
	source, err := os.Open(path)
	if err != nil {
		return nil, nil, 0, "", err
	}
	defer source.Close()
	opened, err := source.Stat()
	if err != nil {
		return nil, nil, 0, "", err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, nil, 0, "", errors.New("input changed before it could be snapshotted")
	}

	snapshot, err := createReadOnlyTemp(filepath.Dir(path), ".native-review-snapshot-")
	if err != nil {
		return nil, nil, 0, "", err
	}
	temporaryPath := snapshot.Name()
	installed := false
	retained := false
	defer func() {
		if !retained {
			_ = snapshot.Close()
		}
		if !installed {
			_ = os.Remove(temporaryPath)
		}
	}()

	digest := sha256.New()
	writers := []io.Writer{snapshot, digest}
	var captured bytes.Buffer
	if capture {
		writers = append(writers, &captured)
	}
	reader := io.Reader(source)
	if maxBytes > 0 {
		reader = io.LimitReader(source, maxBytes+1)
	}
	bytesRead, err := io.Copy(io.MultiWriter(writers...), reader)
	if err != nil {
		return nil, nil, 0, "", err
	}
	if maxBytes > 0 && bytesRead > maxBytes {
		return nil, nil, 0, "", fmt.Errorf("input exceeds %d bytes", maxBytes)
	}
	digestText := hex.EncodeToString(digest.Sum(nil))
	after, err := source.Stat()
	if err != nil {
		return nil, nil, 0, "", err
	}
	if !os.SameFile(opened, after) || opened.Size() != after.Size() || bytesRead != after.Size() {
		return nil, nil, 0, "", errors.New("input changed while it was being snapshotted")
	}
	verifiedBytes, verifiedDigest, err := hashFile(source)
	if err != nil {
		return nil, nil, 0, "", err
	}
	if verifiedBytes != bytesRead || verifiedDigest != digestText {
		return nil, nil, 0, "", errors.New("input changed while its snapshot was being verified")
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, nil, 0, "", err
	}
	if !pathInfo.Mode().IsRegular() || !os.SameFile(pathInfo, opened) {
		return nil, nil, 0, "", errors.New("input path changed while its snapshot was being prepared")
	}
	if err := snapshot.Sync(); err != nil {
		return nil, nil, 0, "", err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return nil, nil, 0, "", err
	}
	installed = true
	if err := validateLockedArtifact(path, snapshot, bytesRead, digestText); err != nil {
		return nil, nil, 0, "", err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return nil, nil, 0, "", err
	}
	retained = true
	return snapshot, captured.Bytes(), bytesRead, digestText, nil
}

func hashFile(file *os.File) (int64, string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, "", err
	}
	digest := sha256.New()
	bytesRead, err := io.Copy(digest, file)
	if err != nil {
		return 0, "", err
	}
	return bytesRead, hex.EncodeToString(digest.Sum(nil)), nil
}

func validateLockedArtifact(path string, file *os.File, expectedBytes int64, expectedSHA string) error {
	if err := validateLockedArtifactPath(path, file, expectedBytes); err != nil {
		return err
	}
	bytesRead, digest, err := hashFile(file)
	if err != nil {
		return err
	}
	if bytesRead != expectedBytes || digest != expectedSHA {
		return errors.New("raw artifact no longer matches its freeze manifest entry")
	}
	return validateLockedArtifactPath(path, file, expectedBytes)
}

func validateLockedArtifactPath(path string, file *os.File, expectedBytes int64) error {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}
	if !pathInfo.Mode().IsRegular() || !os.SameFile(pathInfo, fileInfo) || pathInfo.Size() != expectedBytes ||
		fileInfo.Size() != expectedBytes || fileInfo.Mode().Perm() != 0o400 {
		return errors.New("raw artifact path no longer names the locked inode")
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return err
	}
	return directory.Close()
}

func createReadOnlyTemp(directoryPath string, prefix string) (*os.File, error) {
	for attempt := 0; attempt < 100; attempt++ {
		var randomSuffix [16]byte
		if _, err := rand.Read(randomSuffix[:]); err != nil {
			return nil, err
		}
		path := filepath.Join(directoryPath, prefix+hex.EncodeToString(randomSuffix[:]))
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o400)
		if err == nil {
			if err := file.Chmod(0o400); err != nil {
				file.Close()
				_ = os.Remove(path)
				return nil, err
			}
			return file, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	return nil, errors.New("could not allocate an exclusive temporary artifact")
}

func writeDurableFreezeManifest(path string, manifest NativeFreezeManifest, validate func() error) error {
	directoryPath := filepath.Dir(path)
	temporary, err := createReadOnlyTemp(directoryPath, ".native-review-freeze-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	temporaryRemoved := false
	defer func() {
		_ = temporary.Close()
		if !temporaryRemoved {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := quality.EncodeJSON(temporary, manifest); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	expectedBytes, expectedSHA, err := hashFile(temporary)
	if err != nil {
		return err
	}
	if err := validateLockedArtifact(temporaryPath, temporary, expectedBytes, expectedSHA); err != nil {
		return err
	}
	if err := validate(); err != nil {
		return err
	}
	if err := validateLockedArtifact(temporaryPath, temporary, expectedBytes, expectedSHA); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		return err
	}
	if err := validateLockedArtifact(path, temporary, expectedBytes, expectedSHA); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return err
	}
	temporaryRemoved = true
	return syncDirectory(directoryPath)
}
