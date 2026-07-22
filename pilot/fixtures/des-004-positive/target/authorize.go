package main

import (
	"errors"
	"os"
)

type MembershipStore struct {
	authoritative map[string]bool
	replica       map[string]bool
}

func (store MembershipStore) AuthorityAllows(user string) bool { return store.authoritative[user] }
func (store MembershipStore) ReplicaAllows(user string) bool   { return store.replica[user] }

func AuthorizeTransfer(store MembershipStore, user string) bool {
	return store.ReplicaAllows(user)
}

func HandlePublicTransfer(store MembershipStore, user string) error {
	if !AuthorizeTransfer(store, user) {
		return errors.New("forbidden")
	}
	return nil
}

func main() {
	store := MembershipStore{
		authoritative: map[string]bool{"revoked-user": false},
		replica:       map[string]bool{"revoked-user": true},
	}
	if HandlePublicTransfer(store, "revoked-user") == nil {
		os.Exit(2)
	}
}
