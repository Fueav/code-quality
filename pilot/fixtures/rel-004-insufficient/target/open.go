package resource

type resource interface{}

type wrapper interface {
	Open() (resource, error)
}

func use(source wrapper) error {
	_, err := source.Open()
	return err
}

func handle(source wrapper) error { return use(source) }
