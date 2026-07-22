package batch

type Queue interface {
	Enqueue(ids []string) error
}

type Store interface {
	Update(id string) error
}

func Handle(queue Queue, _ Store, ids []string) error {
	return queue.Enqueue(ids)
}

func ServeRequest(queue Queue, store Store, ids []string) error {
	return Handle(queue, store, ids)
}
