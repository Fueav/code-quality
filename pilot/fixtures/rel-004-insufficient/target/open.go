package resource

type Resource interface{}

type Wrapper interface {
	Open() (Resource, error)
}

func Use(wrapper Wrapper) error {
	_, err := wrapper.Open()
	return err
}
