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

func freezeNativeArtifacts(layout reviewsession.Layout) (frozenNativeArtifacts, error) {
	frozen := frozenNativeArtifacts{Manifest: NativeFreezeManifest{SchemaVersion: 1, Artifacts: []FrozenArtifact{}}}
	specs := []rawArtifactSpec{
		{name: "final_message", path: layout.NativeReviewPath, set: func(target *frozenNativeArtifacts, raw []byte) { target.FinalMessage = raw }},
		{name: "jsonl_stdout", path: layout.NativeStdoutPath},
		{name: "stderr", path: layout.NativeStderrPath},
	}
	present := make([]string, 0, len(specs))
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
		if err := syncRawArtifact(spec.path); err != nil {
			return frozenNativeArtifacts{}, fmt.Errorf("sync raw %s: %w", spec.name, err)
		}
		present = append(present, spec.path)
	}
	if err := writeDurableFreezeManifest(layout.NativeFreezePath, frozen.Manifest); err != nil {
		return frozenNativeArtifacts{}, fmt.Errorf("write raw freeze manifest: %w", err)
	}
	for _, artifact := range present {
		if err := os.Chmod(artifact, 0o400); err != nil {
			return frozenNativeArtifacts{}, fmt.Errorf("make raw artifact read-only: %w", err)
		}
	}
	if err := os.Chmod(layout.NativeFreezePath, 0o400); err != nil {
		return frozenNativeArtifacts{}, fmt.Errorf("make raw freeze manifest read-only: %w", err)
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
	digest := sha256.New()
	bytesRead, err := io.Copy(digest, file)
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
	return bytesRead, hex.EncodeToString(digest.Sum(nil)), nil
}

func syncRawArtifact(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func writeDurableFreezeManifest(path string, manifest NativeFreezeManifest) error {
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
	if err := temporary.Close(); err != nil {
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
