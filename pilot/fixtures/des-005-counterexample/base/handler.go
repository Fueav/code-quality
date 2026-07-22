package batch

const maxRequestItems = 5

type Request struct {
	IDs [maxRequestItems]string
}

func validate(string) error { return nil }

func Handle(request Request) error {
	return validate(request.IDs[0])
}
