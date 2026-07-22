package fetch

type Body interface {
	Close() error
}

type Response struct{ Body Body }

type Transport interface {
	Fetch() (Response, error)
}

func Fetch(transport Transport) error {
	response, err := transport.Fetch()
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return nil
}
