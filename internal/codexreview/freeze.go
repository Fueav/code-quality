package codexreview

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	reviewsession "github.com/Fueav/code-quality/internal/session"
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
		present = append(present, spec.path)
	}
	if err := writeExclusiveJSON(layout.NativeFreezePath, frozen.Manifest); err != nil {
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
