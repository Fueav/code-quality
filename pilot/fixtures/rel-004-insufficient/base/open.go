package resource

type resource interface {
	Close() error
}

type opener interface {
	Open() (resource, error)
}

func use(source opener) error {
	value, err := source.Open()
	if err != nil {
		return err
	}
	defer value.Close()
	return nil
}

func handle(source opener) error { return use(source) }
