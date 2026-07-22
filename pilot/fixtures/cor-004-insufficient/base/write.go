package write

type Operation interface {
	Write() error
}

type Transaction interface {
	Do(first, second Operation) error
}

func Save(transaction Transaction, first, second Operation) error {
	return transaction.Do(first, second)
}
