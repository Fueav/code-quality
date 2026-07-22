package records

import "errors"

var ErrDuplicate = errors.New("duplicate")

type Store interface {
	Exists(value string) bool
	Insert(value string) error
}

func InsertUnique(store Store, value string) error {
	if store.Exists(value) {
		return nil
	}
	return store.Insert(value)
}
