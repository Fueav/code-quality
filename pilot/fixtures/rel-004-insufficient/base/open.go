package resource

type Resource interface {
	Close() error
}

type Opener interface {
	Open() (Resource, error)
}

func Use(opener Opener) error {
	resource, err := opener.Open()
	if err != nil {
		return err
	}
	defer resource.Close()
	return nil
}
