package codexreview

import (
	"errors"
	"os"
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
	return currentAccountHomeDirectory()
}

func acquireNativeReviewLeaseAt(directory string) (*NativeReviewLease, error) {
	file, err := os.Open(directory)
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
	pathInfo, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !fileInfo.IsDir() || !pathInfo.IsDir() || pathInfo.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(fileInfo, pathInfo) || !ownedByCurrentUser(fileInfo) ||
		fileInfo.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("native review lease target is not an owner-controlled directory")
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
