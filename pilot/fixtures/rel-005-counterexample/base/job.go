package job

type Handler interface {
	Handle(error) error
}

func Run(handler Handler, err error) error {
	return handler.Handle(err)
}
