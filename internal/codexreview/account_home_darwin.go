//go:build darwin

package codexreview

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
)

func currentAccountHomeDirectory() (string, error) {
	uid := strconv.Itoa(os.Getuid())
	account, err := user.LookupId(uid)
	if err != nil {
		return "", fmt.Errorf("look up current operating-system account: %w", err)
	}
	if account.Uid != uid {
		return "", fmt.Errorf("operating-system account uid = %q, want %q", account.Uid, uid)
	}
	return validateAccountHomeDirectory(account.HomeDir)
}
