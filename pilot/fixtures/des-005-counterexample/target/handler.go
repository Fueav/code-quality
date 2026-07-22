package batch

const maxRequestItems = 8

type Request struct {
	IDs [maxRequestItems]string
}

type Store interface {
	Update(id string) error
}

func Handle(store Store, request Request) error {
	for _, id := range request.IDs {
		if id == "" {
			continue
		}
		if err := store.Update(id); err != nil {
			return err
		}
	}
	return nil
}
