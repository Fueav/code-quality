package batch

type Store interface {
	Update(id string) error
}

func Handle(store Store, ids []string) error {
	for _, id := range ids {
		if err := store.Update(id); err != nil {
			return err
		}
	}
	return nil
}
