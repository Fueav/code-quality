package nativereview

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

var ErrRestrictedResumeActive = errors.New("another process owns this review session")

type sessionLock struct {
	file *os.File
}

func acquireSessionLock(sessionDir string) (*sessionLock, error) {
	info, err := os.Lstat(sessionDir)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !filepath.IsAbs(sessionDir) || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm() != 0o700 || !ok || stat.Uid != uint32(os.Getuid()) {
		return nil, errors.New("session lock directory is not owner-controlled")
	}
	path := filepath.Join(sessionDir, "session.lock")
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	fileInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	fileStat, fileOK := fileInfo.Sys().(*syscall.Stat_t)
	if !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 || pathInfo.Mode().Perm() != 0o600 ||
		!os.SameFile(pathInfo, fileInfo) || !fileOK || fileStat.Uid != uint32(os.Getuid()) || fileStat.Nlink != 1 {
		return nil, errors.New("session lock file is not a private regular file")
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrRestrictedResumeActive
		}
		return nil, err
	}
	closeOnError = false
	return &sessionLock{file: file}, nil
}

func (lock *sessionLock) inheritedFile() *os.File {
	if lock == nil {
		return nil
	}
	return lock.file
}

func (lock *sessionLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	return file.Close()
}
