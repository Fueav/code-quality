package write

type Operation interface {
	Write() error
}

type Transaction interface {
	Do(first, second Operation) error
}

func Save(_ Transaction, first, second Operation) error {
	if err := first.Write(); err != nil {
		return err
	}
	return second.Write()
}
