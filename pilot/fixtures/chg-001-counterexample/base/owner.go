package owner

import "crypto/sha256"

type Store struct{ values map[[32]byte]string }

func v1Key(input string) [32]byte { return sha256.Sum256([]byte(input)) }

func (store *Store) Write(input, value string) {
	store.values[v1Key(input)] = value
}

func (store Store) Read(input string) (string, bool) {
	value, ok := store.values[v1Key(input)]
	return value, ok
}
