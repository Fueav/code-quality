package batch

const maxRequestItems = 5

type Request struct {
	IDs [maxRequestItems]string
}

func validate(string) error { return nil }

func Handle(request Request) error {
	for _, id := range request.IDs {
		if id == "" {
			continue
		}
		if err := validate(id); err != nil {
			return err
		}
	}
	return nil
}
