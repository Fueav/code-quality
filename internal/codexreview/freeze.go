package codexreview

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	reviewsession "github.com/Fueav/code-quality/internal/session"
	"github.com/Fueav/code-quality/quality"
)

type FrozenArtifact struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Present bool   `json:"present"`
	Bytes   int    `json:"bytes"`
	SHA256  string `json:"sha256,omitempty"`
}

type NativeFreezeManifest struct {
	SchemaVersion int              `json:"schema_version"`
	Artifacts     []FrozenArtifact `json:"artifacts"`
}

type frozenNativeArtifacts struct {
	FinalMessage []byte
	Stdout       []byte
	Stderr       []byte
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
		{name: "jsonl_stdout", path: layout.NativeStdoutPath, set: func(target *frozenNativeArtifacts, raw []byte) { target.Stdout = raw }},
		{name: "stderr", path: layout.NativeStderrPath, set: func(target *frozenNativeArtifacts, raw []byte) { target.Stderr = raw }},
	}
	present := make([]string, 0, len(specs))
	for _, spec := range specs {
		entry := FrozenArtifact{Name: spec.name, Path: filepath.Base(spec.path)}
		raw, err := reviewsession.ReadRegularFile(spec.path, maxNativeOutputBytes)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return frozenNativeArtifacts{}, fmt.Errorf("read raw %s: %w", spec.name, err)
			}
			frozen.Manifest.Artifacts = append(frozen.Manifest.Artifacts, entry)
			continue
		}
		digest := sha256.Sum256(raw)
		entry.Present = true
		entry.Bytes = len(raw)
		entry.SHA256 = hex.EncodeToString(digest[:])
		frozen.Manifest.Artifacts = append(frozen.Manifest.Artifacts, entry)
		spec.set(&frozen, raw)
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
