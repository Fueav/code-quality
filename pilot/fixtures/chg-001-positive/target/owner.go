package main

import (
	"crypto/sha256"
	"os"
	"strings"
)

type Store struct{ owners map[[32]byte]string }

func OwnerKey(email string) [32]byte {
	return sha256.Sum256([]byte(strings.ToLower(email)))
}

func LookupOwner(store Store, email string) (string, bool) {
	owner, ok := store.owners[OwnerKey(email)]
	return owner, ok
}

func main() {
	persistedEmail := "Owner@Example.com"
	store := Store{owners: map[[32]byte]string{
		sha256.Sum256([]byte(persistedEmail)): "resource-1",
	}}
	if _, ok := LookupOwner(store, persistedEmail); !ok {
		os.Exit(2)
	}
}
