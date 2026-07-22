package batch

type Queue interface {
	Enqueue(ids []string) error
}

type Store interface {
	Update(id string) error
}

func Handle(_ Queue, store Store, ids []string) error {
	for _, id := range ids {
		if err := store.Update(id); err != nil {
			return err
		}
	}
	return nil
}

func ServeRequest(queue Queue, store Store, ids []string) error {
	return Handle(queue, store, ids)
}
