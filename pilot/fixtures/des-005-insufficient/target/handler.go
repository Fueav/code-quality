package batch

type BatchQueue interface {
	EnqueueReindex(ids []string) error
}

type LocalIndexer interface {
	Rebuild(id string) error
}

func Handle(_ BatchQueue, indexer LocalIndexer, ids []string) error {
	for _, id := range ids {
		if err := indexer.Rebuild(id); err != nil {
			return err
		}
	}
	return nil
}

func ServeRequest(queue BatchQueue, indexer LocalIndexer, ids []string) error {
	return Handle(queue, indexer, ids)
}
