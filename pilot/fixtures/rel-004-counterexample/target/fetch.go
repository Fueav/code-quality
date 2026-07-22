package fetch

type Body interface {
	Consume() error
	Close() error
}

type Response struct{ Body Body }

type Transport interface {
	Fetch() (Response, error)
}

type Framework struct{ transport Transport }

func (framework Framework) Fetch() error {
	response, err := framework.transport.Fetch()
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return response.Body.Consume()
}

func Fetch(framework Framework) error {
	return framework.Fetch()
}
