package batch

type BatchQueue interface {
	EnqueueReindex(ids []string) error
}

type LocalIndexer interface {
	Rebuild(id string) error
}

func Handle(queue BatchQueue, _ LocalIndexer, ids []string) error {
	return queue.EnqueueReindex(ids)
}

func ServeRequest(queue BatchQueue, indexer LocalIndexer, ids []string) error {
	return Handle(queue, indexer, ids)
}
