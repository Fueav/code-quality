package main

const (
	productionRequestItems = 100_000
	databasePoolSize       = 50
)

type Item struct{ release <-chan struct{} }

type ConnectionPool chan struct{}

func (pool ConnectionPool) use(item Item) {
	pool <- struct{}{}
	defer func() { <-pool }()
	<-item.release
}

func HandlePublicRequest(pool ConnectionPool, items []Item) {
	for _, item := range items {
		go pool.use(item)
	}
}

func main() {
	blockedDownstream := make(chan struct{})
	items := make([]Item, productionRequestItems)
	for index := range items {
		items[index].release = blockedDownstream
	}
	HandlePublicRequest(make(ConnectionPool, databasePoolSize), items)
	select {}
}
