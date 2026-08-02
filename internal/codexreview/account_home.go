package codexreview

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func lookupPasswdHome(reader io.Reader, uid int) (string, error) {
	wanted := strconv.Itoa(uid)
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.SplitN(line, ":", 7)
		if len(fields) < 6 || fields[2] != wanted {
			continue
		}
		if fields[5] == "" {
			return "", fmt.Errorf("operating-system account for uid %d has no home directory", uid)
		}
		return fields[5], nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read operating-system accounts: %w", err)
	}
	return "", fmt.Errorf("operating-system account for uid %d is absent from /etc/passwd", uid)
}

func validateAccountHomeDirectory(raw string) (string, error) {
	homeDirectory := filepath.Clean(raw)
	if !filepath.IsAbs(homeDirectory) || homeDirectory == string(filepath.Separator) {
		return "", errors.New("operating-system account home must be an absolute non-root path")
	}
	resolved, err := filepath.EvalSymlinks(homeDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve operating-system account home: %w", err)
	}
	resolved = filepath.Clean(resolved)
	if !filepath.IsAbs(resolved) || resolved == string(filepath.Separator) {
		return "", errors.New("resolved operating-system account home must be an absolute non-root path")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || !ownedByCurrentUser(info) || info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("operating-system account home is not an owner-controlled directory")
	}
	return resolved, nil
}
