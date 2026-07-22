package fetch

type Body interface {
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
	return nil
}

func Fetch(transport Transport) error {
	return (Framework{transport: transport}).Fetch()
}
