package codexreview

import (
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
}

type rawArtifactSpec struct {
	name string
	path string
	set  func(*frozenNativeArtifacts, []byte)
}

type lockedRawArtifact struct {
	path string
	file *os.File
}

func freezeNativeArtifacts(layout reviewsession.Layout) (frozenNativeArtifacts, error) {
	frozen := frozenNativeArtifacts{Manifest: NativeFreezeManifest{SchemaVersion: 1, Artifacts: []FrozenArtifact{}}}
	specs := []rawArtifactSpec{
		{name: "final_message", path: layout.NativeReviewPath, set: func(target *frozenNativeArtifacts, raw []byte) { target.FinalMessage = raw }},
		{name: "jsonl_stdout", path: layout.NativeStdoutPath},
		{name: "stderr", path: layout.NativeStderrPath},
	}
	for _, spec := range specs {
		entry := FrozenArtifact{Name: spec.name, Path: filepath.Base(spec.path)}
		var err error
		if spec.set != nil {
			var raw []byte
			raw, err = reviewsession.ReadRegularFile(spec.path, maxNativeOutputBytes)
			if err == nil {
				digest := sha256.Sum256(raw)
				entry.Bytes = int64(len(raw))
				entry.SHA256 = hex.EncodeToString(digest[:])
				spec.set(&frozen, raw)
			}
		} else {
			entry.Bytes, entry.SHA256, err = hashRegularFile(spec.path)
		}
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return frozenNativeArtifacts{}, fmt.Errorf("read raw %s: %w", spec.name, err)
			}
			frozen.Manifest.Artifacts = append(frozen.Manifest.Artifacts, entry)
			continue
		}
		entry.Present = true
		frozen.Manifest.Artifacts = append(frozen.Manifest.Artifacts, entry)
	}
	locked := make([]lockedRawArtifact, 0, len(specs))
	defer func() {
		for _, artifact := range locked {
			_ = artifact.file.Close()
		}
	}()
	for index, spec := range specs {
		entry := frozen.Manifest.Artifacts[index]
		if !entry.Present {
			continue
		}
		file, err := lockRawArtifact(spec.path, entry.Bytes, entry.SHA256)
		if err != nil {
			return frozenNativeArtifacts{}, fmt.Errorf("lock raw %s: %w", spec.name, err)
		}
		locked = append(locked, lockedRawArtifact{path: spec.path, file: file})
	}
	validateLockedPaths := func() error {
		for _, artifact := range locked {
			if err := validateLockedRawArtifact(artifact.path, artifact.file); err != nil {
				return err
			}
		}
		return nil
	}
	if err := writeDurableFreezeManifest(layout.NativeFreezePath, frozen.Manifest, validateLockedPaths); err != nil {
		return frozenNativeArtifacts{}, fmt.Errorf("write raw freeze manifest: %w", err)
	}
	return frozen, nil
}

func hashRegularFile(path string) (int64, string, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return 0, "", err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return 0, "", errors.New("input must be a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return 0, "", err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return 0, "", errors.New("input changed while it was being read")
	}
	bytesRead, digestText, err := hashFile(file)
	if err != nil {
		return 0, "", err
	}
	after, err := file.Stat()
	if err != nil {
		return 0, "", err
	}
	if !os.SameFile(opened, after) || opened.Size() != after.Size() || bytesRead != after.Size() {
		return 0, "", errors.New("input changed while it was being read")
	}
	return bytesRead, digestText, nil
}

func lockRawArtifact(path string, expectedBytes int64, expectedSHA string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("input must be a regular non-symlink file")
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	locked := false
	defer func() {
		if !locked {
			_ = file.Close()
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, errors.New("input changed before it could be locked")
	}
	bytesRead, digest, err := hashFile(file)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(opened, after) || opened.Size() != after.Size() || bytesRead != expectedBytes || digest != expectedSHA {
		return nil, errors.New("input no longer matches its freeze manifest entry")
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	if err := file.Chmod(0o400); err != nil {
		return nil, err
	}
	if err := validateLockedRawArtifact(path, file); err != nil {
		return nil, err
	}
	locked = true
	return file, nil
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

func validateLockedRawArtifact(path string, file *os.File) error {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}
	if !pathInfo.Mode().IsRegular() || !os.SameFile(pathInfo, fileInfo) || pathInfo.Size() != fileInfo.Size() || fileInfo.Mode().Perm() != 0o400 {
		return errors.New("raw artifact path no longer names the locked inode")
	}
	return nil
}

func writeDurableFreezeManifest(path string, manifest NativeFreezeManifest, validate func() error) error {
	directoryPath := filepath.Dir(path)
	temporary, err := os.CreateTemp(directoryPath, ".native-review-freeze-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	installed := false
	defer func() {
		_ = temporary.Close()
		if !installed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := quality.EncodeJSON(temporary, manifest); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Chmod(0o400); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := validate(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return err
	}
	installed = true
	directory, err := os.Open(directoryPath)
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return err
	}
	return directory.Close()
}
