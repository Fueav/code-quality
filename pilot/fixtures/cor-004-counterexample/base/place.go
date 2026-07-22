package orders

import "sync"

type Order struct{ ID string }
type Event struct{ ID string }

type Database struct {
	mu     sync.Mutex
	orders map[string]Order
	outbox map[string]Event
}

type Provider struct {
	mu         sync.Mutex
	deliveries map[string]int
}

type Transaction interface {
	Save(Order, Event) error
}

type directTransaction struct {
	database *Database
	provider *Provider
}

func (transaction *directTransaction) Save(order Order, event Event) error {
	transaction.database.mu.Lock()
	transaction.database.orders[order.ID] = order
	transaction.database.mu.Unlock()
	transaction.provider.Deliver(event)
	return nil
}

func Place(transaction Transaction, order Order, event Event) error {
	return transaction.Save(order, event)
}

func (provider *Provider) Deliver(event Event) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.deliveries[event.ID]++
}
