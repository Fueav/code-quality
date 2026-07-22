package batch

type Queue interface {
	Enqueue(ids []string) error
}

func Handle(queue Queue, ids []string) error {
	return queue.Enqueue(ids)
}
