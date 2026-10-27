package reviewrunner

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/Fueav/code-quality/internal/nativereview"
)

type PackageOptions struct {
	RunPath      string
	EvidenceRoot string
	Destination  string
}

type PackageResult struct {
	SchemaVersion int    `json:"schema_version"`
	EvidenceDir   string `json:"evidence_dir"`
	ArchivePath   string `json:"archive_path"`
	ArchiveSHA256 string `json:"archive_sha256"`
	SummaryPath   string `json:"summary_path"`
	Reused        bool   `json:"reused"`
}

type archiveFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type archiveRecord struct {
	SchemaVersion int    `json:"schema_version"`
	InputDigest   string `json:"input_digest"`
	ArchiveSHA256 string `json:"archive_sha256"`
}

// PackageEvidence archives only the exact session named by this invocation.
// It never finds an arbitrary first summary or synthesizes a review result.
func PackageEvidence(options PackageOptions) (PackageResult, error) {
	var result PackageResult
	for _, path := range []string{options.RunPath, options.EvidenceRoot, options.Destination} {
		if !filepath.IsAbs(path) {
			return result, errors.New("package paths must be absolute")
		}
	}
	runRaw, err := readArtifact(options.RunPath, 16<<20)
	if err != nil {
		return result, err
	}
	var document struct {
		EvidenceDir string `json:"evidence_dir"`
		SummaryPath string `json:"summary_path"`
		SessionDir  string `json:"session_dir"`
		Session     struct {
			SessionDir string `json:"session_dir"`
		} `json:"session"`
	}
	if err := json.Unmarshal(runRaw, &document); err != nil {
		return result, err
	}
	session := document.EvidenceDir
	if session == "" {
		session = document.SessionDir
	}
	if session == "" {
		session = document.Session.SessionDir
	}
	root, err := filepath.EvalSymlinks(options.EvidenceRoot)
	if err != nil {
		return result, err
	}
	if err := privateDirectory(session); err != nil {
		return result, err
	}
	session, err = filepath.EvalSymlinks(session)
	if err != nil {
		return result, err
	}
	if session == root || session == "" {
		return result, errors.New("run has no exact review session to archive")
	}
	if err := insidePrivateDirectory(root, session); err != nil {
		return result, err
	}
	checkpointRaw, err := readArtifact(filepath.Join(session, "checkpoint.json"), 16<<20)
	if err != nil {
		return result, fmt.Errorf("session checkpoint is required for packaging: %w", err)
	}
	var checkpoint nativereview.SessionCheckpoint
	if err := json.Unmarshal(checkpointRaw, &checkpoint); err != nil {
		return result, err
	}
	if checkpoint.SchemaVersion != 1 || checkpoint.Plan.ReviewKey == "" {
		return result, errors.New("evidence directory is not a review session")
	}
	summary := filepath.Join(session, "output", "review-summary.md")
	if document.SummaryPath != "" {
		provided, err := canonicalDestination(document.SummaryPath)
		if err != nil {
			return result, err
		}
		if provided != summary {
			return result, errors.New("summary path does not belong to the indexed session")
		}
	}
	files, err := snapshotArchive(session)
	if err != nil {
		return result, err
	}
	inputDigest := hashValue(files)
	cache := filepath.Join(root, "archives")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return result, err
	}
	if err := insidePrivateDirectory(root, cache); err != nil {
		return result, err
	}
	cacheKey := hashValue([]string{session, inputDigest})
	archivePath := filepath.Join(cache, cacheKey+".tar.gz")
	manifestPath := filepath.Join(cache, cacheKey+".json")
	// Serialize packaging per immutable snapshot, including separate CLI calls.
	lock, err := os.OpenFile(filepath.Join(cache, cacheKey+".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return result, err
	}
	defer lock.Close()
	info, err := lock.Stat()
	if err != nil {
		return result, err
	}
	if err := privateFileInfo(info); err != nil {
		return result, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return result, err
	}
	var manifest archiveRecord
	raw, readErr := readPrivate(manifestPath, 1<<20)
	if readErr == nil {
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return result, err
		}
		if manifest.SchemaVersion != 1 || manifest.InputDigest != inputDigest {
			return result, errors.New("archive manifest does not match the session snapshot")
		}
		digest, err := hashArtifact(archivePath)
		if err != nil {
			return result, err
		}
		if digest != manifest.ArchiveSHA256 {
			return result, errors.New("cached evidence archive is corrupt")
		}
		result.Reused = true
	} else if errors.Is(readErr, os.ErrNotExist) {
		if _, err := os.Lstat(archivePath); err == nil {
			return result, errors.New("archive exists without its manifest; refusing uncertain reuse")
		}
		if err := createArchive(session, archivePath, files); err != nil {
			return result, err
		}
		after, err := snapshotArchive(session)
		if err != nil {
			return result, err
		}
		if hashValue(after) != inputDigest {
			return result, errors.New("session changed while its archive was created")
		}
		digest, err := hashArtifact(archivePath)
		if err != nil {
			return result, err
		}
		manifest = archiveRecord{SchemaVersion: 1, InputDigest: inputDigest, ArchiveSHA256: digest}
		encoded, _ := json.Marshal(manifest)
		if err := writeAtomic(manifestPath, encoded); err != nil {
			return result, err
		}
	} else {
		return result, readErr
	}
	destination, err := canonicalDestination(options.Destination)
	if err != nil {
		return result, err
	}
	if isWithin(session, destination) {
		return result, errors.New("artifact destination cannot be inside the source session")
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return result, err
	}
	if err := privateDirectory(destination); err != nil {
		return result, err
	}
	if err := copyArtifact(archivePath, filepath.Join(destination, "evidence.tar.gz")); err != nil {
		return result, err
	}
	if err := writeAtomic(filepath.Join(destination, "run.json"), runRaw); err != nil {
		return result, err
	}
	for _, name := range []string{"review-summary.md", "review-summary.json"} {
		source := filepath.Join(session, "output", name)
		if _, err := os.Lstat(source); errors.Is(err, os.ErrNotExist) {
			stale := filepath.Join(destination, name)
			if info, oldErr := os.Lstat(stale); oldErr == nil {
				if err := privateFileInfo(info); err != nil {
					return result, err
				}
				if err := os.Remove(stale); err != nil {
					return result, err
				}
			} else if !errors.Is(oldErr, os.ErrNotExist) {
				return result, oldErr
			}
			continue
		} else if err != nil {
			return result, err
		}
		if err := copyArtifact(source, filepath.Join(destination, name)); err != nil {
			return result, err
		}
	}
	result.SchemaVersion = 1
	result.EvidenceDir = session
	result.ArchivePath = filepath.Join(destination, "evidence.tar.gz")
	result.ArchiveSHA256 = manifest.ArchiveSHA256
	if _, err := os.Lstat(summary); err == nil {
		result.SummaryPath = filepath.Join(destination, "review-summary.md")
	}
	encoded, _ := json.Marshal(result)
	if err := writeAtomic(filepath.Join(destination, "package.json"), encoded); err != nil {
		return result, err
	}
	return result, nil
}

func snapshotArchive(root string) ([]archiveFile, error) {
	files := []archiveFile{}
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("evidence archive cannot follow symlinks")
		}
		if entry.IsDir() {
			return privateDirectory(path)
		}
		file, info, err := openArtifact(path)
		if err != nil {
			return err
		}
		defer file.Close()
		total += info.Size()
		if len(files) >= 4096 || total > 2<<30 {
			return errors.New("evidence archive exceeds its file or byte budget")
		}
		digest, err := hashOpenArtifact(path, file, info)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, archiveFile{Path: filepath.ToSlash(relative), Size: info.Size(), SHA256: digest})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}

func createArchive(root, path string, files []archiveFile) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".archive-")
	if err != nil {
		return err
	}
	defer func() { _ = temporary.Close(); _ = os.Remove(temporary.Name()) }()
	zip := gzip.NewWriter(temporary)
	archive := tar.NewWriter(zip)
	for _, record := range files {
		source := filepath.Join(root, filepath.FromSlash(record.Path))
		file, info, err := openArtifact(source)
		if err != nil {
			return err
		}
		if info.Size() != record.Size {
			file.Close()
			return errors.New("evidence changed before packaging")
		}
		if err := archive.WriteHeader(&tar.Header{Name: record.Path, Mode: int64(info.Mode().Perm()), Size: record.Size, Typeflag: tar.TypeReg}); err != nil {
			file.Close()
			return err
		}
		hasher := sha256.New()
		_, copyErr := io.CopyN(io.MultiWriter(archive, hasher), file, record.Size)
		verifyErr := unchangedArtifact(source, file, info)
		file.Close()
		if copyErr != nil {
			return copyErr
		}
		if verifyErr != nil {
			return verifyErr
		}
		if hex.EncodeToString(hasher.Sum(nil)) != record.SHA256 {
			return errors.New("evidence digest changed while packaging")
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := zip.Close(); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

func openArtifact(path string) (*os.File, os.FileInfo, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || !owned(info) || info.Mode().Perm()&0o022 != 0 {
		file.Close()
		return nil, nil, errors.New("artifact must be an owner-controlled regular file")
	}
	return file, info, nil
}

func unchangedArtifact(path string, file *os.File, before os.FileInfo) error {
	after, err := file.Stat()
	if err != nil {
		return err
	}
	named, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, named) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return errors.New("artifact changed while reading")
	}
	return nil
}

func hashOpenArtifact(path string, file *os.File, info os.FileInfo) (string, error) {
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	if err := unchangedArtifact(path, file, info); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func hashArtifact(path string) (string, error) {
	file, info, err := openArtifact(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return hashOpenArtifact(path, file, info)
}

func readArtifact(path string, limit int64) ([]byte, error) {
	file, info, err := openArtifact(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info.Size() > limit {
		return nil, errors.New("artifact exceeds read limit")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("artifact exceeds read limit")
	}
	if err := unchangedArtifact(path, file, info); err != nil {
		return nil, err
	}
	return raw, nil
}

func copyArtifact(source, destination string) error {
	file, info, err := openArtifact(source)
	if err != nil {
		return err
	}
	defer file.Close()
	// Archive copies stream through an exclusive temporary file; no large
	// compressed artifact is retained in memory.
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".copy-")
	if err != nil {
		return err
	}
	defer func() { _ = temporary.Close(); _ = os.Remove(temporary.Name()) }()
	if _, err := io.Copy(temporary, file); err != nil {
		return err
	}
	if err := unchangedArtifact(source, file, info); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if prior, err := os.Lstat(destination); err == nil {
		if err := privateFileInfo(prior); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(temporary.Name(), destination)
}
