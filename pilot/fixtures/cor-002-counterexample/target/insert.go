package records

import "errors"

var ErrUniqueConstraint = errors.New("unique constraint")

type Transaction interface {
	Insert(value string) error
}

func Insert(transaction Transaction, value string) error {
	err := transaction.Insert(value)
	if errors.Is(err, ErrUniqueConstraint) {
		return nil
	}
	return err
}
