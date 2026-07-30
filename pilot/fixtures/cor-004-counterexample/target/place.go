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

const reconciliationBatchSize = 64

type outboxTransaction struct{ database *Database }

type outboxItem struct {
	id    string
	event Event
}

func NewOutboxTransaction(database *Database) Transaction {
	return &outboxTransaction{database: database}
}

func (transaction *outboxTransaction) Save(order Order, event Event) error {
	database := transaction.database
	database.mu.Lock()
	defer database.mu.Unlock()
	database.orders[order.ID] = order
	database.outbox[event.ID] = event
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

func nextOutboxBatch(database *Database) []outboxItem {
	database.mu.Lock()
	defer database.mu.Unlock()
	batch := make([]outboxItem, 0, reconciliationBatchSize)
	for id, event := range database.outbox {
		batch = append(batch, outboxItem{id: id, event: event})
		if len(batch) == reconciliationBatchSize {
			break
		}
	}
	return batch
}

func ReconcileOutbox(database *Database, provider *Provider) {
	for {
		batch := nextOutboxBatch(database)
		if len(batch) == 0 {
			return
		}
		for _, item := range batch {
			provider.Deliver(item.event)
			database.mu.Lock()
			delete(database.outbox, item.id)
			database.mu.Unlock()
		}
	}
}
