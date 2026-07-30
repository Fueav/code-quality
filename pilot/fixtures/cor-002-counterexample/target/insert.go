package records

import "errors"

var ErrDuplicate = errors.New("duplicate")

type Store interface {
	Exists(value string) bool
	Insert(value string) error
}

func InsertUnique(store Store, value string) error {
	err := store.Insert(value)
	if errors.Is(err, ErrDuplicate) {
		return nil
	}
	return err
}
