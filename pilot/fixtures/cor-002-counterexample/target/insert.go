package records

import "errors"

var ErrDuplicate = errors.New("duplicate")
var ErrUniqueConstraint = errors.New("unique constraint")

type Store interface {
	Exists(value string) bool
	Insert(value string) error
}

func InsertUnique(store Store, value string) error {
	err := store.Insert(value)
	if errors.Is(err, ErrUniqueConstraint) {
		return nil
	}
	return err
}
