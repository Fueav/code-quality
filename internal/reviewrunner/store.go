package reviewrunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Fueav/code-quality/internal/nativereview"
	"github.com/Fueav/code-quality/internal/reviewplan"
	"github.com/Fueav/code-quality/quality"
)

type requestRecord struct {
	SchemaVersion        int    `json:"schema_version"`
	Key                  string `json:"key"`
	FullReviewKey        string `json:"full_review_key"`
	ReviewKey            string `json:"review_key"`
	BaseTip              string `json:"base_tip"`
	Head                 string `json:"head"`
	RunnerPolicyVersion  string `json:"runner_policy_version"`
	ExecutionFingerprint string `json:"execution_fingerprint"`
	RunRoot              string `json:"run_root"`
	SessionDir           string `json:"session_dir"`
	State                string `json:"state"`
	ParentResult         string `json:"parent_result"`
	Digest               string `json:"digest"`
}

func (record requestRecord) matches(plan reviewplan.Decision, options Options) error {
	if record.FullReviewKey != plan.ReviewKey || record.BaseTip != plan.BaseTipCommit || record.Head != plan.CurrentHead ||
		record.RunnerPolicyVersion != options.RunnerPolicyVersion || record.ExecutionFingerprint != options.ExecutionFingerprint {
		return errors.New("stored request does not match the complete review identity and execution environment")
	}
	return nil
}

func familyKey(plan reviewplan.Decision) string {
	identity := struct{ Repository, HeadRef, Kind, ChangeID string }{Repository: plan.Request.Repository, HeadRef: plan.HeadRef}
	if change := plan.Request.Change; change != nil && change.Kind != "" && change.ID != "" {
		identity.Kind, identity.ChangeID = change.Kind, change.ID
		identity.HeadRef = ""
	}
	return hashValue(identity)
}

func requestKey(reviewKey, policy, fingerprint string) string {
	return hashValue([]string{reviewKey, policy, fingerprint})
}

func hashValue(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func openStore(path, repository string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("state root must be absolute and outside the repository")
	}
	root, err := canonicalDestination(path)
	if err != nil {
		return "", err
	}
	repo, err := filepath.Abs(repository)
	if err != nil {
		return "", err
	}
	repo, err = filepath.EvalSymlinks(repo)
	if err != nil {
		return "", err
	}
	if isWithin(repo, root) {
		return "", errors.New("state root must be outside the reviewed repository")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if err := privateDirectory(root); err != nil {
		return "", err
	}
	return root, nil
}

func canonicalDestination(path string) (string, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("state root must not be a symlink")
	}
	parent := filepath.Clean(path)
	var missing []string
	for {
		_, err := os.Lstat(parent)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return "", err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		missing = append(missing, filepath.Base(parent))
		parent = filepath.Dir(parent)
	}
}

type familyStore struct {
	root string
	lock *os.File
}

func openFamily(ctx context.Context, root, key string, progress io.Writer) (*familyStore, error) {
	path := filepath.Join(root, key)
	if err := os.MkdirAll(filepath.Join(path, "requests"), 0o700); err != nil {
		return nil, err
	}
	if err := insidePrivateDirectory(root, filepath.Join(path, "requests")); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(path, "lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	valid := false
	defer func() {
		if !valid {
			_ = lock.Close()
		}
	}()
	info, err := lock.Stat()
	if err != nil {
		return nil, err
	}
	if err := privateFileInfo(info); err != nil {
		return nil, err
	}
	announced := false
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return nil, err
		}
		if !announced && progress != nil {
			_, _ = fmt.Fprintln(progress, "quality-review: waiting for the active request in this review lineage")
			announced = true
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	after, err := os.Lstat(lockPath)
	if err != nil || !os.SameFile(info, after) {
		return nil, errors.New("coordinator lock changed while acquiring ownership")
	}
	valid = true
	return &familyStore{root: path, lock: lock}, nil
}

func (store *familyStore) Close() error { return store.lock.Close() }

func (store *familyStore) readRecord(key string) (requestRecord, bool, error) {
	if !digestKey(key) {
		return requestRecord{}, false, errors.New("invalid request index key")
	}
	raw, err := readPrivate(filepath.Join(store.root, "requests", key+".json"), 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return requestRecord{}, false, nil
	}
	if err != nil {
		return requestRecord{}, false, err
	}
	record, err := quality.DecodeStrict[requestRecord](bytes.NewReader(raw))
	if err != nil {
		return requestRecord{}, false, err
	}
	digest := record.Digest
	record.Digest = ""
	if record.SchemaVersion != 1 || record.Key != key || digest != hashValue(record) ||
		record.ReviewKey == "" || record.FullReviewKey == "" || record.State == "" ||
		record.RunRoot != filepath.Join(store.root, "runs", key) {
		return requestRecord{}, false, errors.New("request index is corrupt; refusing to start another Native review")
	}
	record.Digest = digest
	if err := insidePrivateDirectory(store.root, record.RunRoot); err != nil {
		return requestRecord{}, false, err
	}
	if record.SessionDir != "" {
		if err := insidePrivateDirectory(record.RunRoot, record.SessionDir); err != nil {
			return requestRecord{}, false, err
		}
	}
	return record, true, nil
}

func (store *familyStore) latest() (requestRecord, bool, error) {
	raw, err := readPrivate(filepath.Join(store.root, "latest"), 128)
	if errors.Is(err, os.ErrNotExist) {
		if _, markerErr := os.Lstat(filepath.Join(store.root, "started")); markerErr == nil {
			return requestRecord{}, false, errors.New("started lineage lost its pointer; automatic FULL is forbidden")
		} else if !errors.Is(markerErr, os.ErrNotExist) {
			return requestRecord{}, false, markerErr
		}
		if runs, runErr := os.ReadDir(filepath.Join(store.root, "runs")); runErr == nil && len(runs) != 0 {
			return requestRecord{}, false, errors.New("retained run evidence lost its lineage; automatic FULL is forbidden")
		} else if runErr != nil && !errors.Is(runErr, os.ErrNotExist) {
			return requestRecord{}, false, runErr
		}
		entries, listErr := os.ReadDir(filepath.Join(store.root, "requests"))
		if listErr != nil {
			return requestRecord{}, false, listErr
		}
		if len(entries) != 0 {
			return requestRecord{}, false, errors.New("request lineage pointer is missing; automatic FULL is forbidden")
		}
		return requestRecord{}, false, nil
	}
	if err != nil {
		return requestRecord{}, false, err
	}
	record, exists, err := store.readRecord(string(raw))
	if err != nil {
		return requestRecord{}, false, err
	}
	if !exists {
		return requestRecord{}, false, errors.New("request lineage record is missing; automatic FULL is forbidden")
	}
	return record, true, nil
}

func (store *familyStore) save(record requestRecord, latest bool) error {
	if latest {
		if err := writeAtomic(filepath.Join(store.root, "started"), []byte("1")); err != nil {
			return err
		}
	}
	record.Digest = ""
	record.Digest = hashValue(record)
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(store.root, "requests", record.Key+".json"), raw); err != nil {
		return err
	}
	if latest {
		return writeAtomic(filepath.Join(store.root, "latest"), []byte(record.Key))
	}
	return nil
}

func (store *familyStore) newRunRoot(key string) (string, error) {
	runs := filepath.Join(store.root, "runs")
	if err := os.MkdirAll(runs, 0o700); err != nil {
		return "", err
	}
	if err := insidePrivateDirectory(store.root, runs); err != nil {
		return "", err
	}
	path := filepath.Join(runs, key)
	// A directory left without its STARTED record is uncertain, never a miss.
	if err := os.Mkdir(path, 0o700); err != nil {
		return "", fmt.Errorf("prepare unique request directory: %w", err)
	}
	return path, nil
}

func discoverSession(root, reviewKey string) (string, error) {
	if err := privateDirectory(root); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	var found string
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return "", errors.New("unexpected artifact in interrupted request directory")
		}
		session := filepath.Join(root, entry.Name())
		if err := insidePrivateDirectory(root, session); err != nil {
			return "", err
		}
		raw, err := readPrivate(filepath.Join(session, "checkpoint.json"), 16<<20)
		if err != nil {
			return "", fmt.Errorf("interrupted request has no verifiable checkpoint: %w", err)
		}
		var checkpoint nativereview.SessionCheckpoint
		if err := json.Unmarshal(raw, &checkpoint); err != nil {
			return "", err
		}
		if checkpoint.Plan.ReviewKey != reviewKey || found != "" {
			return "", errors.New("interrupted request has ambiguous or mismatched sessions")
		}
		found = session
	}
	if found == "" {
		return "", errors.New("previous request started without a retained session; automatic FULL is forbidden")
	}
	return found, nil
}

func privateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 || !owned(info) {
		return fmt.Errorf("state directory must be owner-only mode 0700: %s", path)
	}
	return nil
}

func insidePrivateDirectory(root, path string) error {
	if !filepath.IsAbs(path) || !isWithin(root, path) {
		return errors.New("session escaped its request evidence directory")
	}
	if err := privateDirectory(root); err != nil {
		return err
	}
	relative, _ := filepath.Rel(root, path)
	if relative == "." {
		return nil
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if err := privateDirectory(current); err != nil {
			return err
		}
	}
	return nil
}

func isWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func owned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}

func privateFileInfo(info os.FileInfo) error {
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !owned(info) {
		return errors.New("coordinator state must be an owner-only regular file")
	}
	return nil
}

func readPrivate(path string, limit int64) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if err := privateFileInfo(before); err != nil {
		return nil, err
	}
	if before.Size() > limit {
		return nil, errors.New("coordinator state exceeds size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, pathInfo) || before.Size() != after.Size() || len(raw) > int(limit) || !before.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("coordinator state changed while reading")
	}
	return raw, nil
}

func writeAtomic(path string, raw []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if err := privateFileInfo(info); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".state-")
	if err != nil {
		return err
	}
	defer func() { _ = temporary.Close(); _ = os.Remove(temporary.Name()) }()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func digestKey(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
