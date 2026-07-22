package owner

import (
	"crypto/sha256"
	"strings"
)

type Store struct{ values map[[32]byte]string }

func v1Key(input string) [32]byte { return sha256.Sum256([]byte(input)) }
func v2Key(input string) [32]byte { return sha256.Sum256([]byte(strings.ToLower(input))) }

func (store *Store) Write(input, value string) {
	store.values[v1Key(input)] = value
	store.values[v2Key(input)] = value
}

func (store Store) Read(input string) (string, bool) {
	if value, ok := store.values[v2Key(input)]; ok {
		return value, true
	}
	value, ok := store.values[v1Key(input)]
	return value, ok
}
