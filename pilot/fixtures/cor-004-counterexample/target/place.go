package orders

type Order struct{ ID string }
type Event struct{ ID string }

type Database struct {
	orders map[string]Order
	outbox map[string]Event
}

type Transaction struct {
	database *Database
	order    Order
	event    Event
}

func (database *Database) Begin() *Transaction {
	return &Transaction{database: database}
}

func (transaction *Transaction) SaveOrder(order Order)  { transaction.order = order }
func (transaction *Transaction) SaveOutbox(event Event) { transaction.event = event }

func (transaction *Transaction) Commit() {
	transaction.database.orders[transaction.order.ID] = transaction.order
	transaction.database.outbox[transaction.event.ID] = transaction.event
}

func Place(database *Database, order Order, event Event) {
	transaction := database.Begin()
	transaction.SaveOrder(order)
	transaction.SaveOutbox(event)
	transaction.Commit()
}

type Provider struct {
	deliveries map[string]int
}

func (provider *Provider) Deliver(event Event) {
	if provider.deliveries[event.ID] != 0 {
		return
	}
	provider.deliveries[event.ID] = 1
}

func DeliverOutbox(provider *Provider, event Event) {
	provider.Deliver(event)
}
