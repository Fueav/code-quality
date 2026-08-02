package codexreview

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
)

var ErrNativeReviewActive = errors.New("another native review is active for this user")

type NativeReviewLease struct {
	file *os.File
}

func AcquireNativeReviewLease() (*NativeReviewLease, error) {
	directory, err := nativeReviewLeaseDirectory()
	if err != nil {
		return nil, err
	}
	return acquireNativeReviewLeaseAt(directory)
}

func nativeReviewLeaseDirectory() (string, error) {
	cacheDirectory, err := nativeReviewCacheDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDirectory, "code-quality-native-review"), nil
}

func nativeReviewCacheDirectory() (string, error) {
	uid := os.Getuid()
	account, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return "", fmt.Errorf("look up current operating-system account: %w", err)
	}
	homeDirectory := filepath.Clean(account.HomeDir)
	if !filepath.IsAbs(homeDirectory) || homeDirectory == string(filepath.Separator) {
		return "", errors.New("operating-system account home must be an absolute non-root path")
	}
	homeInfo, err := os.Stat(homeDirectory)
	if err != nil {
		return "", err
	}
	if !homeInfo.IsDir() || !ownedByCurrentUser(homeInfo) || homeInfo.Mode().Perm()&0o022 != 0 {
		return "", errors.New("operating-system account home is not an owner-controlled directory")
	}

	cacheDirectory := filepath.Join(homeDirectory, ".cache")
	if runtime.GOOS == "darwin" {
		cacheDirectory = filepath.Join(homeDirectory, "Library", "Caches")
	}
	if err := os.MkdirAll(cacheDirectory, 0o700); err != nil {
		return "", err
	}
	cacheInfo, err := os.Stat(cacheDirectory)
	if err != nil {
		return "", err
	}
	if !cacheInfo.IsDir() || !ownedByCurrentUser(cacheInfo) || cacheInfo.Mode().Perm()&0o022 != 0 {
		return "", errors.New("user cache directory is not an owner-controlled directory")
	}
	return cacheDirectory, nil
}

func acquireNativeReviewLeaseAt(directory string) (*NativeReviewLease, error) {
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 ||
		directoryInfo.Mode().Perm() != 0o700 || !ownedByCurrentUser(directoryInfo) {
		return nil, errors.New("native review lease directory is not a private directory")
	}

	path := filepath.Join(directory, "active.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()
	fileInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fileInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(fileInfo, pathInfo) || !ownedByCurrentUser(fileInfo) {
		return nil, errors.New("native review lease is not a regular file")
	}
	if err := file.Chmod(0o600); err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrNativeReviewActive
		}
		return nil, err
	}
	closeOnError = false
	return &NativeReviewLease{file: file}, nil
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}

func (lease *NativeReviewLease) InheritedFile() *os.File {
	if lease == nil {
		return nil
	}
	return lease.file
}

func (lease *NativeReviewLease) Close() error {
	if lease == nil || lease.file == nil {
		return nil
	}
	file := lease.file
	lease.file = nil
	return file.Close()
}
