package batch

import "sync"

const (
	maxRequestItems = 20
	workerCount     = 4
)

type Item struct{ ID string }

type Request struct {
	Items [maxRequestItems]Item
}

func Process(request Request) {
	workers := make(chan struct{}, workerCount)
	var group sync.WaitGroup
	for _, item := range request.Items {
		if item.ID == "" {
			continue
		}
		workers <- struct{}{}
		group.Add(1)
		go func() {
			defer group.Done()
			defer func() { <-workers }()
		}()
	}
	group.Wait()
}
