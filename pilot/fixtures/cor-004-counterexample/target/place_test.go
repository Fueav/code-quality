package orders

import "testing"

func TestAtomicOutboxAndIdempotentDelivery(t *testing.T) {
	database := &Database{orders: map[string]Order{}, outbox: map[string]Event{}}
	event := Event{ID: "event-1"}
	if err := Place(NewOutboxTransaction(database), Order{ID: "order-1"}, event); err != nil {
		t.Fatal(err)
	}
	if len(database.orders) != 1 || len(database.outbox) != 1 {
		t.Fatal("order and outbox were not committed together")
	}
	provider := &Provider{deliveries: map[string]int{}}
	DeliverOutbox(provider, event)
	DeliverOutbox(provider, event)
	if provider.deliveries[event.ID] != 1 {
		t.Fatal("redelivery produced a duplicate effect")
	}
}
