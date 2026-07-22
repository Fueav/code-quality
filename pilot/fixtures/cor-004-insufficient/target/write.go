package write

type Operation interface {
	Write() error
}

func Save(first, second Operation) error {
	if err := first.Write(); err != nil {
		return err
	}
	return second.Write()
}
