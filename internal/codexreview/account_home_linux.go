//go:build linux

package codexreview

import (
	"fmt"
	"os"
)

func currentAccountHomeDirectory() (string, error) {
	accounts, err := os.Open("/etc/passwd")
	if err != nil {
		return "", fmt.Errorf("open operating-system accounts: %w", err)
	}
	defer accounts.Close()
	homeDirectory, err := lookupPasswdHome(accounts, os.Getuid())
	if err != nil {
		return "", err
	}
	return validateAccountHomeDirectory(homeDirectory)
}
