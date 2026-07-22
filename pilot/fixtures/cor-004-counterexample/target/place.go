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

type outboxTransaction struct{ database *Database }

func NewOutboxTransaction(database *Database) Transaction {
	return &outboxTransaction{database: database}
}

func cloneOrders(source map[string]Order) map[string]Order {
	result := make(map[string]Order, len(source)+1)
	for id, order := range source {
		result[id] = order
	}
	return result
}

func cloneEvents(source map[string]Event) map[string]Event {
	result := make(map[string]Event, len(source)+1)
	for id, event := range source {
		result[id] = event
	}
	return result
}

func (transaction *outboxTransaction) Save(order Order, event Event) error {
	database := transaction.database
	database.mu.Lock()
	defer database.mu.Unlock()
	orders := cloneOrders(database.orders)
	outbox := cloneEvents(database.outbox)
	orders[order.ID] = order
	outbox[event.ID] = event
	database.orders, database.outbox = orders, outbox
	return nil
}

func Place(transaction Transaction, order Order, event Event) error {
	return transaction.Save(order, event)
}

func (provider *Provider) Deliver(event Event) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.deliveries[event.ID] != 0 {
		return
	}
	provider.deliveries[event.ID] = 1
}

func DeliverOutbox(provider *Provider, event Event) {
	provider.Deliver(event)
}
